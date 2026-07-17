package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// registerDecoy adds a shell-like tool that looks like unrestricted command
// execution. When an agent reaches for it, the tool collects feedback about
// what the agent was trying to accomplish, logs it for analysis, and redirects
// toward the appropriate tool (run, find, or a swamp extension).
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

	// Record the attempt with the command for analysis.
	s.metrics.Record(MetricsEvent{
		Tool:    "shell_exec",
		Command: args.Command,
		Allowed: false,
	})

	// Write a detailed feedback entry for later analysis.
	s.logDecoyAttempt(args)

	// Build a helpful redirect message.
	var msg strings.Builder
	msg.WriteString("shell_exec is not available. Use the 'run' tool for whitelisted commands ")
	msg.WriteString("or the 'find' tool for file/directory discovery.\n\n")
	msg.WriteString("If the work requires a command that isn't whitelisted, the answer is usually ")
	msg.WriteString("a swamp extension — not expanding the whitelist. Recurring capability belongs ")
	msg.WriteString("in structured, validated tooling rather than raw shell access.\n\n")
	msg.WriteString("Tell the user what you were trying to accomplish so the tooling can be improved.")

	return errorResult(msg.String())
}

// logDecoyAttempt writes a structured entry to the metrics directory for
// later review of what capabilities agents are reaching for.
func (s *Server) logDecoyAttempt(args decoyArgs) {
	if s.metrics == nil {
		return
	}

	entry := map[string]any{
		"ts":      time.Now().UTC().Format(time.RFC3339),
		"tool":    "shell_exec",
		"command": args.Command,
	}
	if args.Cwd != "" {
		entry["cwd"] = args.Cwd
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	data = append(data, '\n')

	// Append to a dedicated decoy log (separate from metrics for easy review).
	path := s.metrics.dir + "/decoy.jsonl"
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[weir] decoy log: %v\n", err)
		return
	}
	defer f.Close()
	f.Write(data)
}
