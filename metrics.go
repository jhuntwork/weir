package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// MetricsEvent is a single recorded tool invocation.
type MetricsEvent struct {
	Timestamp      string      `json:"ts"`
	Tool           string      `json:"tool"`
	Command        string      `json:"command,omitempty"`
	Allowed        bool        `json:"allowed"`
	Filters        []filterArg `json:"filters,omitempty"`
	ExitCode       *int        `json:"exit_code,omitempty"`
	DurationMs     int64       `json:"duration_ms,omitempty"`
	OutputLinesPre int         `json:"output_lines_pre,omitempty"`
	OutputLinesPost int        `json:"output_lines_post,omitempty"`
	Truncated      bool        `json:"truncated,omitempty"`
	// find-specific fields
	Path        string         `json:"path,omitempty"`
	FindParams  map[string]any `json:"params,omitempty"`
	ResultCount *int           `json:"result_count,omitempty"`
}

// MetricsCollector handles writing metrics events to daily JSONL files.
type MetricsCollector struct {
	dir       string
	retention time.Duration
	mu        sync.Mutex
	file      *os.File
	fileDate  string // YYYY-MM-DD of the currently open file
}

// NewMetricsCollector creates a collector, ensures the directory exists,
// and runs GC to remove stale files.
func NewMetricsCollector(dir string, retention time.Duration) (*MetricsCollector, error) {
	if dir == "" {
		return nil, nil // metrics disabled
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("metrics dir: %w", err)
	}
	mc := &MetricsCollector{
		dir:       dir,
		retention: retention,
	}
	mc.gc()
	return mc, nil
}

// Record appends an event to today's JSONL file.
func (mc *MetricsCollector) Record(event MetricsEvent) {
	if mc == nil {
		return
	}
	if event.Timestamp == "" {
		event.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}

	data, err := json.Marshal(event)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[weir] metrics marshal: %v\n", err)
		return
	}
	data = append(data, '\n')

	mc.mu.Lock()
	defer mc.mu.Unlock()

	today := time.Now().UTC().Format("2006-01-02")
	if err := mc.ensureFile(today); err != nil {
		fmt.Fprintf(os.Stderr, "[weir] metrics file: %v\n", err)
		return
	}

	if _, err := mc.file.Write(data); err != nil {
		fmt.Fprintf(os.Stderr, "[weir] metrics write: %v\n", err)
	}
}

// Close closes the current file handle.
func (mc *MetricsCollector) Close() {
	if mc == nil {
		return
	}
	mc.mu.Lock()
	defer mc.mu.Unlock()
	if mc.file != nil {
		mc.file.Close()
		mc.file = nil
	}
}

// ensureFile opens (or rotates to) the file for the given date.
// Must be called with mc.mu held.
func (mc *MetricsCollector) ensureFile(date string) error {
	if mc.file != nil && mc.fileDate == date {
		return nil
	}
	if mc.file != nil {
		mc.file.Close()
		mc.file = nil
	}

	path := filepath.Join(mc.dir, date+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	mc.file = f
	mc.fileDate = date
	return nil
}

// gc removes metrics files older than the retention period.
func (mc *MetricsCollector) gc() {
	entries, err := os.ReadDir(mc.dir)
	if err != nil {
		return
	}
	cutoff := time.Now().UTC().Add(-mc.retention)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		dateStr := strings.TrimSuffix(name, ".jsonl")
		fileDate, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue // not a date-named file, skip
		}
		if fileDate.Before(cutoff) {
			path := filepath.Join(mc.dir, name)
			if err := os.Remove(path); err != nil {
				fmt.Fprintf(os.Stderr, "[weir] metrics gc: %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "[weir] metrics gc: removed %s\n", name)
			}
		}
	}
}
