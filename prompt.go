package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"
)

// The system prompt is assembled at startup: identity, environment facts,
// workspace instructions (AGENTS.md), and the skill catalog.

const (
	basePrompt = `You are a hands-on multi-app agent. You carry out tasks end to end on the user's behalf, across the apps connected through your skills.

Behavior:
- Be concise and direct. Reply in the language the user writes in.
- Check Available skills first: for anything touching an external app, load the matching skill and follow its workflow instead of hand-rolling raw API calls.
- Verify before acting: when a task depends on facts you do not have, look them up with the shell before acting on guesses.
- For apps without a skill, plain shell (curl, CLIs) is fine.
- Run shell commands one at a time, prefer several short calls over one long && or | chain unless the commands genuinely feed each other.
- When a tool fails, say so plainly and suggest an alternative when one exists.
- Never claim work you did not do or results you did not observe.`
)

const workspaceLimit = 8192

func environmentBlock(workDir string) string {
	zone, offset := time.Now().Zone()
	tz := zone
	if offset != 0 {
		sign := "+"
		if offset < 0 {
			sign = "-"
			offset = -offset
		}
		tz = fmt.Sprintf("%s (UTC%s%02d:%02d)", zone, sign, offset/3600, offset%3600/60)
	}
	return fmt.Sprintf("Environment:\n- OS: %s/%s\n- Working directory: %s\n- Timezone: %s\n\nRelative paths in tool arguments resolve against the working directory unless a cwd is passed.",
		runtime.GOOS, runtime.GOARCH, workDir, tz)
}

func workspaceInstructions(workDir string) string {
	paths := []string{}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		paths = append(paths, filepath.Join(home, ".agents", "AGENTS.md"))
	}
	paths = append(paths, filepath.Join(workDir, "AGENTS.md"))

	var blocks []string
	used := 0
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		content := strings.TrimSpace(string(raw))
		if content == "" {
			continue
		}
		if used+len(content) > workspaceLimit {
			content = cutUTF8(content, workspaceLimit-used) + "\n[…truncated]"
		}
		blocks = append(blocks, content)
		used += len(content)
		if used >= workspaceLimit {
			break
		}
	}
	if len(blocks) == 0 {
		return ""
	}
	return "Workspace instructions:\n" + strings.Join(blocks, "\n\n")
}

func cutUTF8(s string, max int) string {
	for max > 0 && max < len(s) && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max]
}

func buildSystemPrompt(workDir string, skills []Skill) string {
	prompt := basePrompt
	prompt += "\n\n" + environmentBlock(workDir)
	if workspace := workspaceInstructions(workDir); workspace != "" {
		prompt += "\n\n" + workspace
	}
	if block := CatalogBlock(skills); block != "" {
		prompt += "\n\n" + block
	}
	return prompt
}
