package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"time"
)

// JSONL transcripts under runs/ are the audit trail for humans and the
// input the eval harness asserts against, one file per process.

type Transcript struct {
	file *os.File
}

func NewTranscript(dir string) (*Transcript, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	name := time.Now().Format("20060102-150405")
	if _, err := os.Stat(filepath.Join(dir, name+".jsonl")); err == nil {
		name += "-" + fmt.Sprintf("%d", time.Now().Nanosecond()/1_000_000)
	}
	path := filepath.Join(dir, name+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return &Transcript{file: f}, nil
}

func (t *Transcript) Write(eventType string, fields map[string]any) {
	if t == nil || t.file == nil {
		return
	}
	entry := make(map[string]any, len(fields)+2)
	maps.Copy(entry, fields)
	entry["ts"] = time.Now().UTC().Format(time.RFC3339Nano)
	entry["type"] = eventType
	line, err := json.Marshal(entry)
	if err != nil {
		return
	}
	_, _ = t.file.Write(append(line, '\n'))
}

func (t *Transcript) Close() {
	if t != nil && t.file != nil {
		_ = t.file.Close()
		t.file = nil
	}
}
