package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Agent Skills (https://agentskills.io): SKILL.md with YAML frontmatter,
// descriptions land in the system prompt, full instructions load on demand
// via load_skill.

const (
	skillFileName = "SKILL.md"
	maxSkillChars = 48 << 10
	maxDescChars  = 160
	bundleLimit   = 30
)

var skillNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

type Skill struct {
	Name        string
	Description string
	Path        string
}

func skillDirs(workDir string) []string {
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dirs = append(dirs, filepath.Join(home, ".agents", "skills"))
	}
	dirs = append(dirs, filepath.Join(workDir, ".agents", "skills"))
	return dirs
}

// Discover merges skill catalogs, project skills win on name collision.
func Discover(dirs ...string) []Skill {
	byName := make(map[string]Skill)
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() || !skillNamePattern.MatchString(entry.Name()) {
				continue
			}
			path := filepath.Join(dir, entry.Name(), skillFileName)
			raw, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			meta, _ := SplitFrontmatter(string(raw))
			name := meta["name"]
			if name == "" {
				name = entry.Name()
			}
			byName[name] = Skill{
				Name:        name,
				Description: flattenDescription(meta["description"]),
				Path:        path,
			}
		}
	}
	out := make([]Skill, 0, len(byName))
	for _, s := range byName {
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b Skill) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// SplitFrontmatter splits YAML frontmatter from the markdown body, only
// flat key: value pairs and | / > block scalars, which covers the spec.
func SplitFrontmatter(raw string) (map[string]string, string) {
	lines := strings.Split(raw, "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "---" {
		return nil, raw
	}
	meta := make(map[string]string)
	for i := 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "---" || line == "..." {
			return meta, strings.Join(lines[i+1:], "\n")
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) == "" {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		// Block scalars fold indented continuation lines into one value,
		// i is left on the last consumed line.
		switch value {
		case "|", "|-", ">", ">-":
			var parts []string
			for j := i + 1; j < len(lines); j++ {
				next := lines[j]
				if strings.TrimSpace(next) == "" {
					continue
				}
				if next[0] == ' ' || next[0] == '\t' {
					parts = append(parts, strings.TrimSpace(next))
					i = j
					continue
				}
				break
			}
			value = strings.Join(parts, " ")
		}
		if value != "" {
			meta[key] = value
		}
	}
	return nil, raw
}

func flattenDescription(description string) string {
	description = strings.Join(strings.Fields(description), " ")
	runes := []rune(description)
	if len(runes) > maxDescChars {
		return string(runes[:maxDescChars]) + "…"
	}
	return description
}

// CatalogBlock renders the "Available skills" section for the system prompt.
func CatalogBlock(skills []Skill) string {
	if len(skills) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("Available skills:\n")
	for _, s := range skills {
		sb.WriteString("- ")
		sb.WriteString(s.Name)
		if s.Description != "" {
			sb.WriteString(": ")
			sb.WriteString(flattenDescription(s.Description))
		}
		sb.WriteString("\n")
	}
	sb.WriteString("Call load_skill to read a skill's full instructions before following its workflow.")
	return strings.TrimRight(sb.String(), "\n")
}

func newLoadSkillTool(skills []Skill) Tool {
	return Tool{
		Name: "load_skill",
		Description: "Read a skill's full instructions by name. Names and descriptions are listed " +
			"under Available skills in your system prompt.",
		Parameters: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{"name": map[string]any{"type": "string"}},
			"required":             []string{"name"},
			"additionalProperties": false,
		},
		Execute: func(_ context.Context, args json.RawMessage) (string, error) {
			var params struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return "", fmt.Errorf("load_skill: %w", err)
			}
			name := strings.TrimSpace(params.Name)
			index := slices.IndexFunc(skills, func(s Skill) bool { return s.Name == name })
			if index < 0 {
				names := make([]string, 0, len(skills))
				for _, s := range skills {
					names = append(names, s.Name)
				}
				return "", fmt.Errorf("load_skill: unknown skill %q (available: %s)", name, strings.Join(names, ", "))
			}
			raw, err := os.ReadFile(skills[index].Path)
			if err != nil {
				return "", fmt.Errorf("load_skill: %w", err)
			}
			_, body := SplitFrontmatter(string(raw))
			content := strings.TrimSpace(body)
			if len(content) > maxSkillChars {
				content = cutUTF8(content, maxSkillChars) + "\n\n[…truncated]"
			}
			return content + bundleManifest(skills[index].Path), nil
		},
	}
}

// bundleManifest lists files shipped beside SKILL.md, readable on demand.
func bundleManifest(skillPath string) string {
	root := filepath.Dir(skillPath)
	var files []string
	truncated := false
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil || rel == "." {
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") || strings.Count(rel, "/") >= 3 {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || rel == skillFileName {
			return nil
		}
		if len(files) >= bundleLimit {
			truncated = true
			return filepath.SkipAll
		}
		files = append(files, rel)
		return nil
	})
	if err != nil || len(files) == 0 {
		return ""
	}
	slices.Sort(files)
	var sb strings.Builder
	fmt.Fprintf(&sb, "\n\n---\nSkill directory: %s\nBundled files (read with the shell when needed):", root)
	for _, f := range files {
		sb.WriteString("\n  ")
		sb.WriteString(f)
	}
	if truncated {
		sb.WriteString("\n  … more files not listed")
	}
	return sb.String()
}
