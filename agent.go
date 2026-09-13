package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The run loop: stream a model round, execute its tool calls, feed results
// back, repeat until the model stops calling tools. The agent owns history,
// and transient provider failures are retried with backoff.

const maxRounds = 100

const (
	retryAttempts      = 6
	retryBackoffFactor = 2
	retryInitial       = 2 * time.Second
	retryJitterMax     = 0.25
	retryCap           = 30 * time.Second
)

type Callbacks struct {
	OnDelta      func(text string)
	OnToolCall   func(name, args string)
	OnToolResult func(name, result string, err error)
	OnRetry      func(attempt, maxAttempts int, wait time.Duration, message string)
}

type Agent struct {
	Provider   *Provider
	Tools      *Registry
	System     string
	Transcript *Transcript
	Callbacks  Callbacks

	history []Message
}

// Run executes one user turn on the rolling history and returns the final
// assistant text plus token usage across rounds.
func (a *Agent) Run(ctx context.Context, userMessage string) (string, Usage, error) {
	a.Callbacks = normalizeCallbacks(a.Callbacks)
	a.history = append(a.history, Message{Role: RoleUser, Content: userMessage})
	a.write("user_message", map[string]any{"content": userMessage})

	tools := a.Tools.Definitions()
	var reply strings.Builder
	var total Usage

	for range maxRounds {
		text, calls, usage, err := a.roundWithRetries(ctx, a.history, tools)
		if err != nil {
			a.write("error", map[string]any{"message": err.Error()})
			return reply.String(), total, err
		}
		if usage != nil {
			total.PromptTokens += usage.PromptTokens
			total.CompletionTokens += usage.CompletionTokens
		}
		reply.WriteString(text)

		if len(calls) == 0 {
			a.history = append(a.history, Message{Role: RoleAssistant, Content: text})
			a.write("done", map[string]any{
				"prompt_tokens":     total.PromptTokens,
				"completion_tokens": total.CompletionTokens,
			})
			return reply.String(), total, nil
		}

		a.history = append(a.history, Message{Role: RoleAssistant, Content: text, ToolCalls: calls})
		for _, call := range calls {
			result, execErr := a.executeCall(ctx, call)
			a.history = append(a.history, Message{Role: RoleTool, Content: result, ToolCallID: call.ID})
			a.Callbacks.OnToolResult(call.Name, result, execErr)
		}
	}
	err := fmt.Errorf("tool round budget of %d exhausted", maxRounds)
	a.write("error", map[string]any{"message": err.Error()})
	return reply.String(), total, err
}

func (a *Agent) executeCall(ctx context.Context, call ToolCall) (string, error) {
	a.Callbacks.OnToolCall(call.Name, call.Arguments)
	a.write("tool_call", map[string]any{"name": call.Name, "arguments": call.Arguments})

	result, err := a.Tools.Execute(ctx, call.Name, call.Arguments)
	errText := ""
	if err != nil {
		errText = err.Error()
		if result == "" {
			result = "tool error: " + errText
		} else {
			result += "\ntool error: " + errText
		}
	}
	a.write("tool_result", map[string]any{"name": call.Name, "result": result, "error": errText})
	return result, err
}

func (a *Agent) write(eventType string, fields map[string]any) {
	if a.Transcript != nil {
		a.Transcript.Write(eventType, fields)
	}
}

func (a *Agent) roundWithRetries(ctx context.Context, messages []Message, tools []Tool) (string, []ToolCall, *Usage, error) {
	for attempt := 1; ; attempt++ {
		text, calls, usage, err := a.round(ctx, messages, tools)
		if err == nil || !retryable(err) || attempt >= retryAttempts {
			return text, calls, usage, err
		}
		wait := retryDelay(attempt, err)
		a.Callbacks.OnRetry(attempt+1, retryAttempts, wait, err.Error())
		a.write("retry", map[string]any{
			"attempt":  attempt + 1,
			"delay_ms": wait.Milliseconds(),
			"error":    err.Error(),
		})
		select {
		case <-ctx.Done():
			return "", nil, nil, ctx.Err()
		case <-time.After(wait):
		}
	}
}

func (a *Agent) round(ctx context.Context, messages []Message, tools []Tool) (string, []ToolCall, *Usage, error) {
	stream, err := a.Provider.Chat(ctx, Request{System: a.System, Messages: messages, Tools: tools})
	if err != nil {
		return "", nil, nil, err
	}
	defer stream.Close()

	var text strings.Builder
	var calls []ToolCall
	var usage *Usage

	for stream.Next() {
		chunk := stream.Current()
		if chunk.Delta != "" {
			text.WriteString(chunk.Delta)
			a.Callbacks.OnDelta(chunk.Delta)
		}
		if chunk.FinishReason != "" {
			calls = chunk.ToolCalls
		}
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
	}
	if err := stream.Err(); err != nil {
		return text.String(), nil, usage, err
	}
	return text.String(), calls, usage, nil
}

// retryPatterns catches the network failures that arrive as plain text
// errors rather than an APIError status.
var retryPatterns = regexp.MustCompile(
	`(?i)rate[-_ ]?limit|too many requests|resource[_ ]exhausted|quota|overload|` +
		`connection (?:reset|refused|lost)|network (?:reset|error)|broken pipe|` +
		`unexpected eof|no such host|server misbehaving|tls handshake timeout|` +
		`stream error: stream id \d+`)

// retryable reports whether the error looks transient, 400s fail fast.
func retryable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if apiErr, ok := errors.AsType[*APIError](err); ok {
		return apiErr.StatusCode == http.StatusRequestTimeout ||
			apiErr.StatusCode == http.StatusConflict ||
			apiErr.StatusCode == http.StatusTooManyRequests ||
			apiErr.StatusCode >= 500
	}
	return retryPatterns.MatchString(err.Error())
}

func retryDelay(attempt int, err error) time.Duration {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.RetryAfter > 0 {
		return min(apiErr.RetryAfter, 2*time.Minute)
	}
	base := retryInitial
	for range attempt - 1 {
		base *= retryBackoffFactor
	}
	jitter := time.Duration(rand.Float64() * retryJitterMax * float64(base)) //nolint:gosec // jitter needs no crypto strength
	return min(base+jitter, retryCap)
}

// parseRetryAfter reads a Retry-After header in seconds or HTTP-date form.
func parseRetryAfter(value string) time.Duration {
	if value == "" {
		return 0
	}
	if secs, err := strconv.ParseFloat(value, 64); err == nil && secs > 0 {
		return min(time.Duration(secs*float64(time.Second)), 2*time.Minute)
	}
	if t, err := http.ParseTime(value); err == nil {
		if d := time.Until(t); d > 0 {
			return min(d, 2*time.Minute)
		}
	}
	return 0
}

// normalizeCallbacks fills unset hooks with no-ops.
func normalizeCallbacks(cbs Callbacks) Callbacks {
	if cbs.OnDelta == nil {
		cbs.OnDelta = func(string) {}
	}
	if cbs.OnToolCall == nil {
		cbs.OnToolCall = func(string, string) {}
	}
	if cbs.OnToolResult == nil {
		cbs.OnToolResult = func(string, string, error) {}
	}
	if cbs.OnRetry == nil {
		cbs.OnRetry = func(int, int, time.Duration, string) {}
	}
	return cbs
}
