package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Tools are plain functions behind a name, the registry is deliberately
// tiny. Two guarantees no tool should reimplement live here: a call cannot
// outlive its deadline, and a result cannot flood the context.

type ToolFunc func(ctx context.Context, args json.RawMessage) (string, error)

type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
	Execute     ToolFunc
	Timeout     time.Duration
	MaxResult   int
}

type Registry struct {
	names []string
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

func (r *Registry) Register(t Tool) error {
	if t.Name == "" {
		return errors.New("tools: tool is missing a name")
	}
	if _, dup := r.tools[t.Name]; dup {
		return fmt.Errorf("tools: duplicate tool %q", t.Name)
	}
	if t.Timeout <= 0 {
		t.Timeout = 10 * time.Minute
	}
	if t.MaxResult <= 0 {
		t.MaxResult = 256 << 10
	}
	r.tools[t.Name] = t
	r.names = append(r.names, t.Name)
	return nil
}

func (r *Registry) Definitions() []Tool {
	out := make([]Tool, 0, len(r.names))
	for _, n := range r.names {
		out = append(out, r.tools[n])
	}
	return out
}

func (r *Registry) Execute(ctx context.Context, name, arguments string) (string, error) {
	t, ok := r.tools[name]
	if !ok {
		return "", fmt.Errorf("tools: unknown tool %q", name)
	}
	if arguments == "" {
		arguments = "{}"
	}
	if !json.Valid([]byte(arguments)) {
		return "", fmt.Errorf("tools: %q received invalid JSON arguments", name)
	}
	ctx, cancel := context.WithTimeout(ctx, t.Timeout)
	defer cancel()
	result, err := t.Execute(ctx, json.RawMessage(arguments))
	return clampResult(result, t.MaxResult), err
}

func clampResult(result string, max int) string {
	if len(result) <= max {
		return result
	}
	cut := cutUTF8(result, max)
	return cut + fmt.Sprintf("\n[output truncated at %d bytes, full result was %d bytes]", len(cut), len(result))
}

const (
	shellDefaultTimeout = 30 * time.Second
	shellMaxTimeout     = 5 * time.Minute
	shellMaxOutput      = 512 << 10
)

// shell runs the commands that skills teach. load_skill only reads
// instructions, so every action that touches an app ends up here.
func newShellTool(workDir string) Tool {
	return Tool{
		Name: "shell",
		Description: "Run a shell command via sh -c and return its combined output. The command " +
			"runs in a clean environment with only PATH and HOME preset, pass anything " +
			"else via env. Commands start in the session working directory, pass cwd to " +
			"run elsewhere, cd does not persist. timeout_seconds defaults to 30, max 300, " +
			"a non-zero exit code is reported in the result, not as an error. Run genuinely " +
			"long tasks in the background and poll the log instead.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command":         map[string]any{"type": "string", "description": "Shell command to run via sh -c"},
				"cwd":             map[string]any{"type": "string", "description": "Directory the command runs in (default: the session working directory)"},
				"env":             map[string]any{"type": "object", "description": "Environment variables for the command. An empty value unsets a variable.", "additionalProperties": map[string]any{"type": "string"}},
				"timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 300},
			},
			"required":             []string{"command"},
			"additionalProperties": false,
		},
		Timeout:   shellMaxTimeout + 10*time.Second,
		MaxResult: shellMaxOutput,
		Execute:   func(ctx context.Context, args json.RawMessage) (string, error) { return runShell(ctx, workDir, args) },
	}
}

func runShell(ctx context.Context, workDir string, args json.RawMessage) (string, error) {
	var params struct {
		Command        string            `json:"command"`
		Cwd            string            `json:"cwd"`
		Env            map[string]string `json:"env"`
		TimeoutSeconds int               `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("shell: %w", err)
	}
	if strings.TrimSpace(params.Command) == "" {
		return "", errors.New("shell: command is required")
	}
	cwd := workDir
	if params.Cwd != "" {
		if filepath.IsAbs(params.Cwd) {
			cwd = params.Cwd
		} else {
			cwd = filepath.Join(workDir, params.Cwd)
		}
	}

	timeout := shellDefaultTimeout
	if params.TimeoutSeconds > 0 {
		timeout = time.Duration(params.TimeoutSeconds) * time.Second
	}
	timeout = min(timeout, shellMaxTimeout)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "sh", "-c", params.Command)
	cmd.Dir = cwd
	cmd.Env = shellEnv(params.Env)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil || cmd.Process.Pid <= 0 {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	var out cappedBuffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	if err == nil {
		return formatShellOutput(out.String(), out.capped, ""), nil
	}
	if runCtx.Err() == context.DeadlineExceeded {
		return formatShellOutput(out.String(), out.capped, fmt.Sprintf("[timed out after %s]", timeout)), nil
	}
	if runCtx.Err() != nil {
		return "", fmt.Errorf("shell: %w", runCtx.Err())
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return formatShellOutput(out.String(), out.capped, fmt.Sprintf("[exit code %d]", exitErr.ExitCode())), nil
	}
	return "", fmt.Errorf("shell: %w", err)
}

func shellEnv(overrides map[string]string) []string {
	m := make(map[string]string, len(overrides)+2)
	if p := os.Getenv("PATH"); p != "" {
		m["PATH"] = p
	}
	if h := os.Getenv("HOME"); h != "" {
		m["HOME"] = h
	}
	for k, v := range overrides {
		if v == "" {
			delete(m, k)
			continue
		}
		m[k] = v
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(m))
	for _, k := range keys {
		env = append(env, k+"="+m[k])
	}
	return env
}

type cappedBuffer struct {
	buffer bytes.Buffer
	capped bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	remaining := shellMaxOutput - c.buffer.Len()
	if remaining <= 0 {
		c.capped = true
		return len(p), nil
	}
	if len(p) > remaining {
		c.buffer.Write(p[:remaining])
		c.capped = true
		return len(p), nil
	}
	return c.buffer.Write(p)
}

func (c *cappedBuffer) String() string { return c.buffer.String() }

func formatShellOutput(out string, truncated bool, notice string) string {
	out = strings.TrimSpace(out)
	if out == "" && notice == "" {
		out = "(no output)"
	}
	if truncated {
		out += "\n[output truncated at 512 KiB]"
	}
	if notice != "" {
		out += "\n" + notice
	}
	return out
}
