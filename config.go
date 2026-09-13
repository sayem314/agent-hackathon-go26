package main

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// loadConfig resolves the four AGENT_* keys from the environment, seeded by
// .env, real environment variables always win over file values.

func loadConfig() (baseURL, apiKey, model, effort string, err error) {
	if err := loadDotEnv(envFile); err != nil {
		return "", "", "", "", fmt.Errorf("reading %s: %w", envFile, err)
	}

	baseURL = strings.TrimSpace(os.Getenv("AGENT_BASE_URL"))
	apiKey = strings.TrimSpace(os.Getenv("AGENT_API_KEY"))
	model = strings.TrimSpace(os.Getenv("AGENT_MODEL"))
	effort = strings.TrimSpace(os.Getenv("AGENT_EFFORT"))

	if baseURL == "" {
		return "", "", "", "", fmt.Errorf("AGENT_BASE_URL is not set, add it to .env")
	}
	if model == "" {
		return "", "", "", "", fmt.Errorf("AGENT_MODEL is not set, add it to .env")
	}
	if effort != "" {
		switch effort {
		case "minimal", "low", "medium", "high", "xhigh", "max":
		default:
			return "", "", "", "", fmt.Errorf("AGENT_EFFORT must be one of minimal, low, medium, high, xhigh, max (got %q)", effort)
		}
	}
	return baseURL, apiKey, model, effort, nil
}

// loadDotEnv seeds the environment from a .env file, a missing file is fine.
func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		// drop one layer of matching surrounding quotes
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, value)
		}
	}
	return scanner.Err()
}
