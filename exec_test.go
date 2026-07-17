package main

import (
	"context"
	"strings"
	"testing"
)

func testConfig() *Config {
	return &Config{
		Commands: map[string]CommandConfig{
			"echo": {},
			"sh":   {},
		},
		Defaults: DefaultConfig{
			MaxOutputLines: 100,
			Timeout:        10,
		},
	}
}

func TestExecuteAllowed(t *testing.T) {
	cfg := testConfig()
	parsed := &Parsed{
		Command: "echo",
		Args:    []string{"hello", "world"},
	}

	result, err := Execute(context.Background(), cfg, parsed, 10, 1000, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code: got %d, want 0", result.ExitCode)
	}
	if strings.TrimSpace(result.Stdout) != "hello world" {
		t.Fatalf("stdout: got %q, want %q", result.Stdout, "hello world\n")
	}
}

func TestExecuteBlocked(t *testing.T) {
	cfg := testConfig()
	parsed := &Parsed{
		Command: "rm",
		Args:    []string{"-rf", "/"},
	}

	_, err := Execute(context.Background(), cfg, parsed, 10, 1000, "")
	if err == nil {
		t.Fatal("expected error for blocked command")
	}
	if !strings.Contains(err.Error(), "not in the whitelist") {
		t.Fatalf("expected whitelist error, got: %v", err)
	}
}

func TestExecuteWithEnv(t *testing.T) {
	cfg := testConfig()
	parsed := &Parsed{
		Command: "sh",
		Args:    []string{"-c", "echo $TEST_VAR"},
		Env:     map[string]string{"TEST_VAR": "weir_test"},
	}

	result, err := Execute(context.Background(), cfg, parsed, 10, 1000, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(result.Stdout) != "weir_test" {
		t.Fatalf("stdout: got %q, want %q", result.Stdout, "weir_test")
	}
}

func TestExecuteWithFilters(t *testing.T) {
	cfg := testConfig()
	parsed := &Parsed{
		Command: "sh",
		Args:    []string{"-c", "seq 1 5"},
		Filters: []Filter{
			{Type: FilterTail, Arg: "2"},
		},
	}

	result, err := Execute(context.Background(), cfg, parsed, 10, 1000, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Filtered {
		t.Fatal("expected Filtered=true")
	}
	want := "4\n5"
	if strings.TrimSpace(result.Stdout) != want {
		t.Fatalf("got %q, want %q", result.Stdout, want)
	}
}

func TestExecuteNonZeroExit(t *testing.T) {
	cfg := testConfig()
	parsed := &Parsed{
		Command: "sh",
		Args:    []string{"-c", "exit 42"},
	}

	result, err := Execute(context.Background(), cfg, parsed, 10, 1000, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != 42 {
		t.Fatalf("exit code: got %d, want 42", result.ExitCode)
	}
}

func TestExecuteTimeout(t *testing.T) {
	cfg := testConfig()
	parsed := &Parsed{
		Command: "sh",
		Args:    []string{"-c", "sleep 10"},
	}

	result, err := Execute(context.Background(), cfg, parsed, 1, 1000, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != -1 {
		t.Fatalf("exit code: got %d, want -1 (timeout)", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "timed out") {
		t.Fatalf("stderr should mention timeout, got: %q", result.Stderr)
	}
}

func TestExecuteTruncation(t *testing.T) {
	cfg := &Config{
		Commands: map[string]CommandConfig{
			"sh": {},
		},
		Defaults: DefaultConfig{
			MaxOutputLines: 3,
			Timeout:        10,
		},
	}
	parsed := &Parsed{
		Command: "sh",
		Args:    []string{"-c", "seq 1 10"},
	}

	result, err := Execute(context.Background(), cfg, parsed, 10, 3, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Truncated {
		t.Fatal("expected Truncated=true")
	}
	lines := strings.Split(strings.TrimRight(result.Stdout, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines after truncation, got %d: %v", len(lines), lines)
	}
}

func TestExecuteFilteredNotPreTruncated(t *testing.T) {
	cfg := testConfig()
	// maxLines=3 should NOT truncate before the filter sees the data.
	// The grep filter selects line "8" from a 10-line input — which is
	// beyond the 3-line limit. If pre-truncation were active, this would
	// return empty.
	parsed := &Parsed{
		Command: "sh",
		Args:    []string{"-c", "seq 1 10"},
		Filters: []Filter{
			{Type: FilterGrep, Arg: "^8$"},
		},
	}

	result, err := Execute(context.Background(), cfg, parsed, 10, 3, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Filtered {
		t.Fatal("expected Filtered=true")
	}
	if result.Truncated {
		t.Fatal("expected Truncated=false — filters bypass pre-truncation")
	}
	want := "8"
	if strings.TrimSpace(result.Stdout) != want {
		t.Fatalf("got %q, want %q — filter should see full output regardless of maxLines", result.Stdout, want)
	}
}
