package main

import (
	"context"
	"strings"
	"testing"
)

// These tests prove that shell metacharacters have no special meaning
// in weir's direct execution model. Pipes, semicolons, and other shell
// operators are passed as literal arguments to the command — they don't
// invoke a shell or chain commands.

func TestPipeIsLiteralArg(t *testing.T) {
	cfg := testConfig()
	// "echo hello | cat" — if a shell were involved, this would pipe.
	// In direct execution, "|" and "cat" are just arguments to echo.
	parsed := &Parsed{
		Command: "echo",
		Args:    []string{"hello", "|", "cat"},
	}

	result, err := Execute(context.Background(), cfg, parsed, 10, 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code: got %d, want 0", result.ExitCode)
	}
	// echo prints all args literally — the pipe symbol is just text.
	if strings.TrimSpace(result.Stdout) != "hello | cat" {
		t.Fatalf("stdout: got %q, want %q", strings.TrimSpace(result.Stdout), "hello | cat")
	}
}

func TestSemicolonIsLiteralArg(t *testing.T) {
	cfg := testConfig()
	// "echo hello ; echo world" — if a shell were involved, two commands run.
	// In direct execution, ";" and "echo" and "world" are just arguments.
	parsed := &Parsed{
		Command: "echo",
		Args:    []string{"hello", ";", "echo", "world"},
	}

	result, err := Execute(context.Background(), cfg, parsed, 10, 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code: got %d, want 0", result.ExitCode)
	}
	if strings.TrimSpace(result.Stdout) != "hello ; echo world" {
		t.Fatalf("stdout: got %q, want %q", strings.TrimSpace(result.Stdout), "hello ; echo world")
	}
}

func TestDoubleAmpersandIsLiteralArg(t *testing.T) {
	cfg := testConfig()
	parsed := &Parsed{
		Command: "echo",
		Args:    []string{"hello", "&&", "echo", "world"},
	}

	result, err := Execute(context.Background(), cfg, parsed, 10, 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code: got %d, want 0", result.ExitCode)
	}
	if strings.TrimSpace(result.Stdout) != "hello && echo world" {
		t.Fatalf("stdout: got %q, want %q", strings.TrimSpace(result.Stdout), "hello && echo world")
	}
}

func TestBacktickIsLiteralArg(t *testing.T) {
	cfg := testConfig()
	parsed := &Parsed{
		Command: "echo",
		Args:    []string{"`whoami`"},
	}

	result, err := Execute(context.Background(), cfg, parsed, 10, 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code: got %d, want 0", result.ExitCode)
	}
	// Backticks are NOT interpreted — printed literally.
	if strings.TrimSpace(result.Stdout) != "`whoami`" {
		t.Fatalf("stdout: got %q, want %q", strings.TrimSpace(result.Stdout), "`whoami`")
	}
}

func TestDollarParenIsLiteralArg(t *testing.T) {
	cfg := testConfig()
	parsed := &Parsed{
		Command: "echo",
		Args:    []string{"$(whoami)"},
	}

	result, err := Execute(context.Background(), cfg, parsed, 10, 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code: got %d, want 0", result.ExitCode)
	}
	// Command substitution is NOT interpreted — printed literally.
	if strings.TrimSpace(result.Stdout) != "$(whoami)" {
		t.Fatalf("stdout: got %q, want %q", strings.TrimSpace(result.Stdout), "$(whoami)")
	}
}
