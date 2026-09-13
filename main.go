package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
)

// agent-hackathon-go26: a slim multi-app agent. One binary, zero
// dependencies, two builtin tools (shell + load_skill), app integrations
// live in skills wrapping CLIs. Help text lives in the usage string.

const envFile = ".env"

func main() {
	os.Exit(run())
}

func run() int {
	args := os.Args[1:]
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Print(usage)
		return 0
	}

	baseURL, apiKey, model, effort, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n%s", err, usage)
		return 2
	}

	workDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "working directory: %s\n", err)
		return 2
	}

	skills := Discover(skillDirs(workDir)...)
	registry := NewRegistry()
	if err := registry.Register(newShellTool(workDir)); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		return 2
	}
	if err := registry.Register(newLoadSkillTool(skills)); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		return 2
	}

	transcript, err := NewTranscript("runs")
	if err != nil {
		fmt.Fprintf(os.Stderr, "transcript: %s\n", err)
		return 2
	}
	defer transcript.Close()

	provider := NewProvider(baseURL, apiKey, model)
	provider.Effort = effort
	agent := &Agent{
		Provider:   provider,
		Tools:      registry,
		System:     buildSystemPrompt(workDir, skills),
		Transcript: transcript,
	}

	ui := NewUI(os.Stdout)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if len(args) == 0 {
		Repl(ctx, agent, ui)
		return 0
	}
	return OneShot(ctx, agent, ui, strings.Join(args, " "))
}

const usage = `agent-hackathon-go26: a slim multi-app agent

Usage:
  agent-hackathon-go26            interactive session
  agent-hackathon-go26 "prompt"   one shot, exit code 0 on success

Configuration (.env in the working directory, environment overrides):
  AGENT_BASE_URL   OpenAI-compatible endpoint (required)
  AGENT_API_KEY    bearer token (optional for local gateways)
  AGENT_MODEL      model name (required)
  AGENT_EFFORT     optional reasoning effort: minimal, low, medium, high, xhigh, max

Tools: shell + load_skill. Skills are discovered in ~/.agents/skills and
./.agents/skills, project skills win on name collisions. Transcripts land
in runs/ as JSONL.
`
