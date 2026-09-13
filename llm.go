package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

// OpenAI-compatible chat completions client, one protocol by design: any
// gateway that speaks the OpenAI streaming wire format works.

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
}

type Usage struct {
	PromptTokens     int64
	CompletionTokens int64
}

func (u Usage) String() string {
	return fmt.Sprintf("%s in / %s out", humanTokens(u.PromptTokens), humanTokens(u.CompletionTokens))
}

func humanTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

type Request struct {
	System   string
	Messages []Message
	Tools    []Tool
}

type Chunk struct {
	Delta        string
	FinishReason string
	ToolCalls    []ToolCall
	Usage        *Usage
}

type Stream interface {
	Next() bool
	Current() Chunk
	Err() error
	Close() error
}

// APIError is a non-2xx response from the gateway.
type APIError struct {
	StatusCode int
	Status     string
	Message    string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		return fmt.Sprintf("provider error: HTTP %d %s", e.StatusCode, e.Status)
	}
	return fmt.Sprintf("provider error: HTTP %d: %s", e.StatusCode, msg)
}

type Provider struct {
	BaseURL string
	APIKey  string
	Model   string
	Effort  string
	HTTP    *http.Client

	// IdleTimeout bounds one silent gap between server bytes, a hung
	// gateway fails fast instead of blocking a turn forever.
	IdleTimeout time.Duration
}

func NewProvider(baseURL, apiKey, model string) *Provider {
	return &Provider{
		BaseURL:     strings.TrimRight(baseURL, "/"),
		APIKey:      apiKey,
		Model:       model,
		HTTP:        &http.Client{},
		IdleTimeout: 5 * time.Minute,
	}
}

func (p *Provider) Chat(ctx context.Context, req Request) (Stream, error) {
	effort := p.Effort
	body := map[string]any{
		"model":    p.Model,
		"messages": requestMessages(req),
		"stream":   true,
		"stream_options": map[string]any{
			"include_usage": true,
		},
	}
	if effort != "" {
		body["reasoning_effort"] = effort
	}
	if len(req.Tools) > 0 {
		defs := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			def := map[string]any{"name": t.Name}
			if t.Description != "" {
				def["description"] = t.Description
			}
			if t.Parameters != nil {
				def["parameters"] = t.Parameters
			}
			defs = append(defs, map[string]any{"type": "function", "function": def})
		}
		body["tools"] = defs
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("llm: encode request: %w", err)
	}

	idle := p.IdleTimeout
	if idle <= 0 {
		idle = 5 * time.Minute
	}
	client := p.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	// The watchdog cancels the request context when bytes stop arriving.
	reqCtx, cancel := context.WithCancelCause(ctx)
	idleErr := fmt.Errorf("llm: stream idle timeout, no data for %s", idle)
	timer := time.AfterFunc(idle, func() { cancel(idleErr) })

	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, p.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		timer.Stop()
		cancel(nil)
		return nil, fmt.Errorf("llm: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := client.Do(httpReq)
	if err != nil {
		timer.Stop()
		cancel(nil)
		if c := context.Cause(reqCtx); c != nil && c != ctx.Err() {
			return nil, c
		}
		return nil, fmt.Errorf("llm: request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		apiErr := &APIError{
			StatusCode: resp.StatusCode,
			Status:     http.StatusText(resp.StatusCode),
			Message:    errorMessage(raw),
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
		}
		timer.Stop()
		cancel(nil)
		return nil, apiErr
	}

	s := &stream{
		cancel:  cancel,
		timer:   timer,
		ctx:     reqCtx,
		scanner: bufio.NewScanner(&watchdogBody{ReadCloser: resp.Body, reset: func() { timer.Reset(idle) }}),
	}
	s.scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	return s, nil
}

// watchdogBody re-arms the idle timer on every successful read, keep-alive
// comments count as life just as chunks do.
type watchdogBody struct {
	io.ReadCloser
	reset func()
}

func (w *watchdogBody) Read(p []byte) (int, error) {
	n, err := w.ReadCloser.Read(p)
	if n > 0 {
		w.reset()
	}
	return n, err
}

type stream struct {
	scanner   *bufio.Scanner
	cancel    context.CancelCauseFunc
	timer     *time.Timer
	ctx       context.Context // cancelled by the watchdog
	current   Chunk
	pending   map[int64]*ToolCall // tool call fragments by upstream index
	usage     *Usage
	done      bool
	streamErr error
}

func (s *stream) Next() bool {
	if s.done {
		return false
	}
	for s.scanner.Scan() {
		data, ok := sseData(s.scanner.Text())
		if !ok {
			continue
		}
		if data == "[DONE]" {
			s.done = true
			return false
		}
		if s.consume(data) {
			return true
		}
		if s.streamErr != nil {
			return false
		}
	}
	if err := s.scanner.Err(); err != nil {
		if c := context.Cause(s.ctx); c != nil {
			s.streamErr = c
		} else {
			s.streamErr = fmt.Errorf("llm: stream read: %w", err)
		}
	}
	s.done = true
	return false
}

// sseData extracts the payload of one SSE data line. Blank lines, comment
// keep-alives, and non-data fields come back with ok false.
func sseData(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, ":") {
		return "", false
	}
	data, ok := strings.CutPrefix(line, "data:")
	if !ok {
		return "", false
	}
	return strings.TrimSpace(data), true
}

// consume folds one data payload into the stream state and reports whether
// it produced a chunk worth emitting, errors end the stream.
func (s *stream) consume(data string) bool {
	var raw sseChunk
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		s.streamErr = fmt.Errorf("llm: bad stream chunk: %w", err)
		s.done = true
		return false
	}
	if raw.Error.Message != "" {
		s.streamErr = fmt.Errorf("llm: stream error: %s", raw.Error.Message)
		s.done = true
		return false
	}
	if raw.Usage != nil {
		s.usage = &Usage{PromptTokens: raw.Usage.PromptTokens, CompletionTokens: raw.Usage.CompletionTokens}
	}
	chunk := Chunk{Usage: s.usage}
	if len(raw.Choices) > 0 {
		choice := raw.Choices[0]
		for _, tc := range choice.Delta.ToolCalls {
			s.accumulate(tc)
		}
		chunk.Delta = choice.Delta.Content
		if choice.FinishReason != nil {
			chunk.FinishReason = *choice.FinishReason
		}
		if chunk.FinishReason != "" {
			chunk.ToolCalls = s.finalize()
		}
	}
	if chunk.Delta != "" || chunk.FinishReason != "" || chunk.Usage != nil {
		s.current = chunk
		return true
	}
	return false
}

func (s *stream) Current() Chunk { return s.current }

func (s *stream) Err() error { return s.streamErr }

func (s *stream) Close() error {
	if s.timer != nil {
		s.timer.Stop()
	}
	s.cancel(nil)
	return nil
}

// accumulate folds one streaming tool-call fragment into the pending set,
// keyed by the upstream call index.
func (s *stream) accumulate(tc sseToolCall) {
	if s.pending == nil {
		s.pending = make(map[int64]*ToolCall)
	}
	call, ok := s.pending[tc.Index]
	if !ok {
		call = &ToolCall{}
		s.pending[tc.Index] = call
	}
	if tc.ID != "" {
		call.ID = tc.ID
	}
	if tc.Function.Name != "" {
		call.Name = tc.Function.Name
	}
	call.Arguments += tc.Function.Arguments
}

// finalize returns the accumulated tool calls in upstream index order.
func (s *stream) finalize() []ToolCall {
	if len(s.pending) == 0 {
		return nil
	}
	indexes := make([]int64, 0, len(s.pending))
	for i := range s.pending {
		indexes = append(indexes, i)
	}
	slices.Sort(indexes)
	calls := make([]ToolCall, 0, len(indexes))
	for _, i := range indexes {
		calls = append(calls, *s.pending[i])
	}
	s.pending = nil
	return calls
}

type sseToolCall struct {
	Index    int64  `json:"index"`
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type sseChunk struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
	Choices []struct {
		Delta struct {
			Content   string        `json:"content"`
			ToolCalls []sseToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
}

func requestMessages(req Request) []map[string]any {
	out := make([]map[string]any, 0, len(req.Messages)+1)
	if req.System != "" {
		out = append(out, map[string]any{"role": "system", "content": req.System})
	}
	for _, m := range req.Messages {
		msg := map[string]any{"role": string(m.Role)}
		switch m.Role {
		case RoleTool:
			msg["content"] = m.Content
			msg["tool_call_id"] = m.ToolCallID
		case RoleAssistant:
			msg["content"] = m.Content
			if len(m.ToolCalls) > 0 {
				msg["tool_calls"] = wireToolCalls(m.ToolCalls)
				if m.Content == "" {
					delete(msg, "content")
				}
			}
		default:
			msg["content"] = m.Content
		}
		out = append(out, msg)
	}
	return out
}

// wireToolCalls renders assistant tool calls in the OpenAI wire shape.
func wireToolCalls(calls []ToolCall) []map[string]any {
	out := make([]map[string]any, 0, len(calls))
	for _, tc := range calls {
		out = append(out, map[string]any{
			"id":   tc.ID,
			"type": "function",
			"function": map[string]any{
				"name":      tc.Name,
				"arguments": tc.Arguments,
			},
		})
	}
	return out
}

func errorMessage(raw []byte) string {
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
		Detail  string `json:"detail"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		trimmed := strings.TrimSpace(string(raw))
		if len(trimmed) > 200 {
			trimmed = trimmed[:200] + "..."
		}
		return trimmed
	}
	switch {
	case body.Error.Message != "":
		return body.Error.Message
	case body.Message != "":
		return body.Message
	case body.Detail != "":
		return body.Detail
	default:
		return ""
	}
}
