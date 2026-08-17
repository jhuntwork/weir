package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestDeniedRunGuidesAgentToSwamp(t *testing.T) {
	cfg := &Config{
		Commands: map[string]CommandConfig{
			"git": {Subcommands: []string{"status", "log", "diff"}},
		},
	}
	server := NewServer(cfg, version, nil)
	result := server.handleRun(context.Background(), json.RawMessage(`{"command":"git config --global --get user.name"}`))

	if !result.IsError {
		t.Fatal("expected denied command to be an error")
	}
	text := result.Content[0].Text
	if !strings.Contains(text, `swamp extension search "git" --content-type models --json`) {
		t.Fatalf("denial should search model extensions by service, got: %q", text)
	}
	if !strings.Contains(text, `swamp extension info <package> --json`) {
		t.Fatalf("denial should require inspecting candidate metadata, got: %q", text)
	}
	if !strings.Contains(text, `swamp model type search "git" --json`) {
		t.Fatalf("denial should search installed model types, got: %q", text)
	}
	if !strings.Contains(text, "extend the closest model type") {
		t.Fatalf("denial should direct model extension only after capability checks, got: %q", text)
	}
}

func TestRunMetricsEventRecordsCompleteRequest(t *testing.T) {
	timeout := 30
	maxLines := 200
	maxBytes := 4096
	args := runArgs{
		Command:  "git status --short",
		Cwd:      "/work/project",
		Env:      map[string]string{"MODE": "test"},
		Filters:  []filterArg{{Type: "head", Expr: "10"}},
		Timeout:  &timeout,
		MaxLines: &maxLines,
		MaxBytes: &maxBytes,
	}
	event := runMetricsEvent(args, "git", []string{"git", "status", "--short"})

	if event.Command != args.Command || event.Executable != "git" || event.Subcommand != "status" {
		t.Fatalf("unexpected command metadata: %#v", event)
	}
	if got, want := strings.Join(event.Arguments, " "), "status --short"; got != want {
		t.Fatalf("arguments: got %q, want %q", got, want)
	}
	if event.Cwd != args.Cwd || event.Env["MODE"] != "test" || event.RequestedTimeout != &timeout || event.RequestedMaxLines != &maxLines || event.RequestedMaxBytes != &maxBytes {
		t.Fatalf("request metadata was not retained: %#v", event)
	}
	if len(event.Filters) != 1 || event.Filters[0].Type != "head" {
		t.Fatalf("filters were not retained: %#v", event.Filters)
	}
}

func TestRunRejectionReasonRequiresSubcommandFirst(t *testing.T) {
	cfg := &Config{Commands: map[string]CommandConfig{
		"git": {Subcommands: []string{"status"}},
	}}
	if got := runRejectionReason(cfg, "git", []string{"--git-dir", "status", "config"}); got != "subcommand_must_be_first" {
		t.Fatalf("got %q, want subcommand_must_be_first", got)
	}
}
