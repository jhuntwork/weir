package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (s *Server) registerTools() {
	allowed := make([]string, 0, len(s.config.Commands))
	for name := range s.config.Commands {
		allowed = append(allowed, name)
	}
	sort.Strings(allowed)

	description := `Execute a whitelisted command with optional environment variables and output filters.

The command's basename must be in the configured allowlist. Filters are applied
left-to-right on stdout, like a pipeline:
  grep   — keep lines matching a regex pattern
  grep_v — exclude lines matching a regex pattern
  jq     — parse output as JSON and apply a jq expression
  head   — keep the first n lines
  tail   — keep the last n lines
  sort   — sort lines (expr: "" for lexicographic, "reverse", "numeric", or "numeric,reverse")
  uniq   — deduplicate adjacent lines (expr: "" for plain, "count" for counts)
  wc     — count lines/words/chars (expr: "lines" (default), "words", or "chars")

Filters can be combined: jq to extract, then grep to narrow, then head to limit.

If a command you need is not in the whitelist, or a filter doesn't behave as
expected, or you find yourself working around a limitation of this tool, tell
the user. This tool is under active development and feedback about missing
commands, unhelpful error messages, or filter behaviors that don't match
expectations is valuable.

If the work requires a command that isn't whitelisted, the answer is usually
a swamp extension — not expanding the whitelist. Recurring capability belongs
in structured, validated tooling rather than raw shell access.

Allowed commands for this session: ` + strings.Join(allowed, ", ")

	s.register(Tool{
		Name:        "run",
		Description: description,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The command to run with its arguments. Example: 'swamp data get tracker all --json'",
				},
				"env": map[string]any{
					"type":        "object",
					"description": "Optional environment variables to set for the command.",
					"additionalProperties": map[string]any{"type": "string"},
				},
				"filters": map[string]any{
					"type":        "array",
					"description": "Optional ordered list of output filters, applied left-to-right.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"type": map[string]any{
								"type":        "string",
								"enum":        []string{"grep", "grep_v", "jq", "head", "tail", "sort", "uniq", "wc"},
								"description": "Filter type.",
							},
							"expr": map[string]any{
								"type":        "string",
								"description": "Pattern (grep), expression (jq), or line count as string (head/tail).",
							},
						},
						"required": []string{"type", "expr"},
					},
				},
				"timeout": map[string]any{
					"type":        "number",
					"description": "Timeout in seconds. 0 for unlimited. Omit to use the configured default.",
				},
				"max_lines": map[string]any{
					"type":        "integer",
					"description": "Maximum output lines to return. 0 for unlimited. Omit to use the configured default.",
				},
			},
			"required": []string{"command"},
		},
	}, s.handleRun)

	s.register(Tool{
		Name: "find",
		Description: `Search for files and directories matching criteria under a given path.

A safe, built-in alternative to the 'find' command. Searches a directory tree
with filters for name pattern, file type, depth, modification time, and size.
No exec, no delete, no shell evaluation — this is a pure query tool.

The path must be a specific directory (not "/" or system roots like /proc, /sys, /dev).`,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Directory to search. Required. Must not be '/' or a system root directory.",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Glob pattern matched against the file's basename. Example: '*.go', 'Makefile', '*.{ts,tsx}'.",
				},
				"type": map[string]any{
					"type":        "string",
					"enum":        []string{"f", "d", "l"},
					"description": "Filter by entry type: 'f' (file), 'd' (directory), 'l' (symlink).",
				},
				"maxdepth": map[string]any{
					"type":        "integer",
					"description": "Maximum directory depth to descend. 1 = immediate children only.",
				},
				"mtime": map[string]any{
					"type":        "string",
					"description": "Filter by modification time. '+7d' = older than 7 days, '-1h' = newer than 1 hour. Units: m(inutes), h(ours), d(ays), w(eeks).",
				},
				"size": map[string]any{
					"type":        "string",
					"description": "Filter by file size. '+1M' = larger than 1 MiB, '-10k' = smaller than 10 KiB. Units: b, k/K, M, G.",
				},
				"exclude": map[string]any{
					"type":        "array",
					"description": "Glob patterns to skip (matched against basename). Example: ['node_modules', '.*', '*.pyc'].",
					"items":       map[string]any{"type": "string"},
				},
			},
			"required": []string{"path"},
		},
	}, s.handleFind)
}

type runArgs struct {
	Command  string            `json:"command"`
	Env      map[string]string `json:"env,omitempty"`
	Filters  []filterArg       `json:"filters,omitempty"`
	Timeout  *int              `json:"timeout,omitempty"`
	MaxLines *int              `json:"max_lines,omitempty"`
}

type filterArg struct {
	Type string `json:"type"`
	Expr string `json:"expr"`
}

func (s *Server) handleRun(ctx context.Context, params json.RawMessage) *ToolResult {
	var args runArgs
	if err := json.Unmarshal(params, &args); err != nil {
		return errorResult("parse params: " + err.Error())
	}
	if args.Command == "" {
		return errorResult("command is required")
	}

	// Parse command string into binary + args.
	parts, err := tokenize(args.Command)
	if err != nil {
		return errorResult("parse command: " + err.Error())
	}
	if len(parts) == 0 {
		return errorResult("empty command")
	}

	basename := filepath.Base(parts[0])

	// Validate against whitelist.
	if !s.config.IsAllowed(basename) {
		s.metrics.Record(MetricsEvent{
			Tool:    "run",
			Command: args.Command,
			Allowed: false,
		})
		return errorResult(fmt.Sprintf(
			"command %q is not in the whitelist. "+
				"If you need this capability, try: swamp extension search %s",
			basename, basename))
	}

	// Validate subcommand if restricted.
	if !s.config.IsSubcommandAllowed(basename, parts[1:]) {
		s.metrics.Record(MetricsEvent{
			Tool:    "run",
			Command: args.Command,
			Allowed: false,
		})
		allowed := s.config.Commands[basename].Subcommands
		return errorResult(fmt.Sprintf("subcommand %q is not allowed for %q (allowed: %s). "+
			"If you need more, try: swamp extension search %s",
			firstArg(parts[1:]), basename, strings.Join(allowed, ", "), basename))
	}

	// Convert filter args to internal Filter type.
	var filters []Filter
	for _, f := range args.Filters {
		ft, err := parseFilterType(f.Type)
		if err != nil {
			return errorResult(err.Error())
		}
		filters = append(filters, Filter{Type: ft, Arg: f.Expr})
	}

	// Build Parsed struct for Execute.
	parsed := &Parsed{
		Env:     args.Env,
		Command: parts[0],
		Args:    parts[1:],
		Filters: filters,
	}

	// Determine timeout.
	timeoutSec := s.config.TimeoutFor(basename)
	if args.Timeout != nil {
		timeoutSec = *args.Timeout
	}

	// Determine max output lines.
	maxLines := s.config.MaxOutputFor(basename)
	if args.MaxLines != nil {
		maxLines = *args.MaxLines
	}

	// Execute with timing.
	start := time.Now()
	result, err := Execute(ctx, s.config, parsed, timeoutSec, maxLines)
	durationMs := time.Since(start).Milliseconds()

	if err != nil {
		s.metrics.Record(MetricsEvent{
			Tool:       "run",
			Command:    args.Command,
			Allowed:    true,
			Filters:    args.Filters,
			DurationMs: durationMs,
		})
		return errorResult(err.Error())
	}

	// Count output lines for metrics.
	outputLines := countLines(result.Stdout)
	exitCode := result.ExitCode

	s.metrics.Record(MetricsEvent{
		Tool:            "run",
		Command:         args.Command,
		Allowed:         true,
		Filters:         args.Filters,
		ExitCode:        &exitCode,
		DurationMs:      durationMs,
		OutputLinesPre:  outputLines,
		OutputLinesPost: outputLines, // same if no filters changed line count at this level
		Truncated:       result.Truncated,
	})

	return textResult(formatResult(result))
}

type findArgs struct {
	Path     string   `json:"path"`
	Name     string   `json:"name,omitempty"`
	Type     string   `json:"type,omitempty"`
	MaxDepth int      `json:"maxdepth,omitempty"`
	Mtime    string   `json:"mtime,omitempty"`
	Size     string   `json:"size,omitempty"`
	Exclude  []string `json:"exclude,omitempty"`
}

func (s *Server) handleFind(_ context.Context, params json.RawMessage) *ToolResult {
	var args findArgs
	if err := json.Unmarshal(params, &args); err != nil {
		return errorResult("parse params: " + err.Error())
	}
	if args.Path == "" {
		return errorResult("path is required")
	}

	// Validate path safety.
	if err := ValidateFindPath(args.Path, s.config.FindDenyPaths); err != nil {
		s.metrics.Record(MetricsEvent{
			Tool:    "find",
			Path:    args.Path,
			Allowed: false,
		})
		return errorResult(err.Error())
	}

	// Validate type if provided.
	if args.Type != "" && args.Type != "f" && args.Type != "d" && args.Type != "l" {
		return errorResult(fmt.Sprintf("invalid type %q: must be 'f', 'd', or 'l'", args.Type))
	}

	// Build params.
	fp := &FindParams{
		Path:     args.Path,
		Name:     args.Name,
		Type:     args.Type,
		MaxDepth: args.MaxDepth,
		Exclude:  args.Exclude,
	}

	// Parse mtime if provided.
	if args.Mtime != "" {
		mtime, err := ParseMtime(args.Mtime)
		if err != nil {
			return errorResult(err.Error())
		}
		fp.Mtime = mtime
	}

	// Parse size if provided.
	if args.Size != "" {
		size, err := ParseSize(args.Size)
		if err != nil {
			return errorResult(err.Error())
		}
		fp.Size = size
	}

	// Check path exists.
	info, err := os.Stat(args.Path)
	if err != nil {
		return errorResult(fmt.Sprintf("path %q: %v", args.Path, err))
	}
	if !info.IsDir() {
		return errorResult(fmt.Sprintf("path %q is not a directory", args.Path))
	}

	// Run find with timing.
	start := time.Now()
	results, err := RunFind(fp)
	durationMs := time.Since(start).Milliseconds()

	if err != nil {
		s.metrics.Record(MetricsEvent{
			Tool:       "find",
			Path:       args.Path,
			Allowed:    true,
			DurationMs: durationMs,
		})
		return errorResult(fmt.Sprintf("find error: %v", err))
	}

	// Record metrics.
	resultCount := len(results)
	findParams := map[string]any{}
	if args.Name != "" {
		findParams["name"] = args.Name
	}
	if args.Type != "" {
		findParams["type"] = args.Type
	}
	if args.MaxDepth > 0 {
		findParams["maxdepth"] = args.MaxDepth
	}
	if args.Mtime != "" {
		findParams["mtime"] = args.Mtime
	}
	if args.Size != "" {
		findParams["size"] = args.Size
	}
	if len(args.Exclude) > 0 {
		findParams["exclude"] = args.Exclude
	}

	s.metrics.Record(MetricsEvent{
		Tool:        "find",
		Path:        args.Path,
		FindParams:  findParams,
		Allowed:     true,
		DurationMs:  durationMs,
		ResultCount: &resultCount,
	})

	// Format output — one path per line, with type/size info.
	if len(results) == 0 {
		return textResult("no matches found")
	}

	var b strings.Builder
	for _, r := range results {
		switch r.Type {
		case "dir":
			fmt.Fprintf(&b, "%s/\n", r.Path)
		case "symlink":
			fmt.Fprintf(&b, "%s@\n", r.Path)
		default:
			fmt.Fprintf(&b, "%s\n", r.Path)
		}
	}
	fmt.Fprintf(&b, "\n--- %d matches\n", len(results))
	return textResult(b.String())
}

func parseFilterType(s string) (FilterType, error) {
	switch strings.ToLower(s) {
	case "grep":
		return FilterGrep, nil
	case "grep_v":
		return FilterGrepV, nil
	case "jq":
		return FilterJQ, nil
	case "head":
		return FilterHead, nil
	case "tail":
		return FilterTail, nil
	case "sort":
		return FilterSort, nil
	case "uniq":
		return FilterUniq, nil
	case "wc":
		return FilterWC, nil
	default:
		return 0, fmt.Errorf("unknown filter type %q (allowed: grep, grep_v, jq, head, tail, sort, uniq, wc)", s)
	}
}

func formatResult(r *RunResult) string {
	var b strings.Builder

	if r.Stdout != "" {
		b.WriteString(r.Stdout)
		if !strings.HasSuffix(r.Stdout, "\n") {
			b.WriteString("\n")
		}
	}

	var meta []string
	if r.ExitCode != 0 {
		meta = append(meta, fmt.Sprintf("exit_code=%d", r.ExitCode))
	}
	if r.Truncated {
		meta = append(meta, "truncated=true")
	}
	if r.Stderr != "" {
		meta = append(meta, fmt.Sprintf("stderr=%s", strings.TrimSpace(r.Stderr)))
	}

	if len(meta) > 0 {
		b.WriteString("\n---\n")
		for _, m := range meta {
			b.WriteString(m)
			b.WriteString("\n")
		}
	}

	return b.String()
}


func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}
