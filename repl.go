package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Terminal output: raw streaming text for replies, dim lines for tool
// activity. Colors degrade to plain text when stdout is not a terminal.

const (
	ansiDim   = "\x1b[2m"
	ansiBold  = "\x1b[1m"
	ansiRed   = "\x1b[31m"
	ansiReset = "\x1b[0m"
)

type UI struct {
	out io.Writer
	tty bool
}

func NewUI(out io.Writer) *UI {
	f, isFile := out.(*os.File)
	return &UI{out: out, tty: isFile && isTerminal(f)}
}

func (u *UI) reply(text string)               { fmt.Fprint(u.out, text) }
func (u *UI) newline()                        { fmt.Fprintln(u.out) }
func (u *UI) note(format string, args ...any) { u.color(ansiDim, format, args...) }
func (u *UI) fail(format string, args ...any) { u.color(ansiRed, format, args...) }
func (u *UI) title(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if u.tty {
		msg = ansiBold + msg + ansiReset
	}
	fmt.Fprintln(u.out, msg)
}

func (u *UI) color(code, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if u.tty {
		fmt.Fprintf(u.out, "%s%s%s", code, msg, ansiReset)
	} else {
		fmt.Fprint(u.out, msg)
	}
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func truncateForDisplay(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// displayCallbacks is pure display, the agent owns the transcript events.
func displayCallbacks(ui *UI) Callbacks {
	return Callbacks{
		OnDelta: func(text string) { ui.reply(text) },
		OnToolCall: func(name, args string) {
			ui.note("→ %s %s\n", name, truncateForDisplay(args, 160))
		},
		OnToolResult: func(name, result string, err error) {
			summary := firstLine(result)
			if err != nil {
				ui.fail("← %s: %s\n", name, truncateForDisplay(summary, 200))
			} else {
				ui.note("← %s %s\n", name, truncateForDisplay(summary, 200))
			}
		},
		OnRetry: func(attempt, maxAttempts int, wait time.Duration, message string) {
			ui.note("[retry %d/%d in %.1fs] %s\n", attempt, maxAttempts, wait.Seconds(), truncateForDisplay(message, 160))
		},
	}
}

// RunTurn executes one user message with streaming display and a footer.
func RunTurn(ctx context.Context, agent *Agent, ui *UI, prompt string) error {
	agent.Callbacks = displayCallbacks(ui)
	started := time.Now()
	_, usage, err := agent.Run(ctx, prompt)
	elapsed := time.Since(started)

	if err != nil {
		ui.newline()
		ui.fail("run failed: %s\n", err.Error())
		return err
	}
	ui.newline()
	ui.note("[%s · %s · %.1fs]\n", agent.Provider.Model, usage.String(), elapsed.Seconds())
	return nil
}

// Repl is the interactive mode: one process, one conversation in memory.
func Repl(ctx context.Context, agent *Agent, ui *UI) {
	ui.title("agent-hackathon-go26 — type a message, /new resets, /quit exits")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				ui.fail("input error: %s\n", err)
			}
			return
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		switch line {
		case "/quit", "/exit":
			return
		case "/new":
			agent.history = nil
			ui.note("started a fresh conversation\n")
			continue
		case "/help":
			ui.note("type a message and press enter, /new resets the conversation, /quit exits\n")
			continue
		}
		_ = RunTurn(ctx, agent, ui, line)
	}
}

// OneShot runs a single prompt headless, the mode the eval harness drives.
// Exit code 0 on success, 1 on failure.
func OneShot(ctx context.Context, agent *Agent, ui *UI, prompt string) int {
	if err := RunTurn(ctx, agent, ui, prompt); err != nil {
		return 1
	}
	return 0
}
