package main

import (
	"context"
	"encoding/json"
)

// registerDecoy adds a shell-like tool that looks like unrestricted command
// execution. It logs what agents attempt and redirects them to use 'run'.
func (s *Server) registerDecoy() {
	s.register(Tool{
		Name: "shell_exec",
		Description: `Execute a shell command and return its output.

Runs the given command string through the system shell (/bin/sh -c).
Supports pipes, redirects, environment variables, and all standard shell features.
Working directory defaults to the user's home unless 'cwd' is specified.

Returns stdout as text. If the command fails, returns stderr with a non-zero exit indication.`,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The shell command to execute. Supports full shell syntax including pipes, redirects, and variable expansion.",
				},
				"cwd": map[string]any{
					"type":        "string",
					"description": "Working directory for the command. Defaults to $HOME if not specified.",
				},
				"timeout": map[string]any{
					"type":        "integer",
					"description": "Maximum execution time in seconds. Defaults to 30.",
				},
			},
			"required": []string{"command"},
		},
	}, s.handleDecoy)
}

type decoyArgs struct {
	Command string `json:"command"`
	Cwd     string `json:"cwd,omitempty"`
	Timeout int    `json:"timeout,omitempty"`
}

func (s *Server) handleDecoy(_ context.Context, params json.RawMessage) *ToolResult {
	var args decoyArgs
	if err := json.Unmarshal(params, &args); err != nil {
		args.Command = string(params) // best-effort capture even on bad parse
	}

	// Record the attempt.
	s.metrics.Record(MetricsEvent{
		Tool:    "shell_exec",
		Command: args.Command,
		Allowed: false,
	})

	return errorResult(
		"shell_exec is not available. Use the 'run' tool for whitelisted commands " +
			"or the 'find' tool for file/directory discovery.",
	)
}
