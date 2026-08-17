package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
				"cwd": map[string]any{
					"type":        "string",
					"description": "Working directory for the command. Defaults to $HOME if not specified.",
				},
				"env": map[string]any{
					"type":                 "object",
					"description":          "Optional environment variables to set for the command.",
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
					"description": "Maximum output lines to return after filters. 0 for unlimited. Omit to use the configured default.",
				},
				"max_bytes": map[string]any{
					"type":        "integer",
					"description": "Maximum output bytes to return after filters. 0 for unlimited. Omit to use the configured default.",
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
	Cwd      string            `json:"cwd,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	Filters  []filterArg       `json:"filters,omitempty"`
	Timeout  *int              `json:"timeout,omitempty"`
	MaxLines *int              `json:"max_lines,omitempty"`
	MaxBytes *int              `json:"max_bytes,omitempty"`
}

type filterArg struct {
	Type string `json:"type"`
	Expr string `json:"expr"`
}

func runMetricsEvent(args runArgs, basename string, parts []string) MetricsEvent {
	event := MetricsEvent{
		Tool:              "run",
		Command:           args.Command,
		Executable:        basename,
		Arguments:         append([]string(nil), parts[1:]...),
		Cwd:               args.Cwd,
		Env:               args.Env,
		RequestedTimeout:  args.Timeout,
		RequestedMaxLines: args.MaxLines,
		RequestedMaxBytes: args.MaxBytes,
		Filters:           args.Filters,
	}
	if len(parts) > 1 && !strings.HasPrefix(parts[1], "-") {
		event.Subcommand = parts[1]
	}
	return event
}

func runRejectionReason(cfg *Config, basename string, args []string) string {
	if !cfg.IsAllowed(basename) {
		return "command_not_allowed"
	}
	if len(cfg.Commands[basename].Subcommands) == 0 {
		return ""
	}
	if len(args) == 0 {
		return "missing_subcommand"
	}
	if strings.HasPrefix(args[0], "-") {
		return "subcommand_must_be_first"
	}
	return "subcommand_not_allowed"
}

func deniedRunResult(cfg *Config, basename string, parts []string, reason string) *ToolResult {
	intent := basename
	if len(parts) > 1 && !strings.HasPrefix(parts[1], "-") {
		intent += " " + parts[1]
	}

	var detail string
	switch reason {
	case "subcommand_must_be_first":
		detail = "Restricted commands must use the canonical form `command subcommand [args...]`; flags before the subcommand are unsupported."
	case "missing_subcommand":
		detail = "This command requires an explicit allowed subcommand."
	case "subcommand_not_allowed":
		detail = fmt.Sprintf("Allowed direct subcommands for %q: %s.", basename, strings.Join(cfg.Commands[basename].Subcommands, ", "))
	default:
		detail = fmt.Sprintf("Command %q is not in Weir's direct allowlist.", basename)
	}

	return errorResult(fmt.Sprintf(
		"The requested operation %q is not available through Weir's intentionally small direct surface. %s\n\n"+
			"Search Swamp model extensions by service (not operation):\n  swamp extension search %s --content-type models --json\n\n"+
			"Prefer @swamp/* candidates, but inspect each candidate's types and methods before pulling:\n  swamp extension info <package> --json\n\n"+
			"Pull only a candidate that exposes the needed type and method. If none does, check installed types:\n  swamp model type search %s --json\n\n"+
			"If no candidate or installed type covers the operation, extend the closest model type; create a new Swamp model extension only when no type fits.",
		intent, detail, strconv.Quote(basename), strconv.Quote(basename),
	))
}

func (s *Server) handleRun(ctx context.Context, params json.RawMessage) *ToolResult {
	var args runArgs
	if err := json.Unmarshal(params, &args); err != nil {
		return errorResult("parse params: " + err.Error())
	}
	if args.Command == "" {
		return errorResult("command is required")
	}

	if args.Timeout != nil && *args.Timeout < 0 {
		return errorResult("timeout must be non-negative")
	}
	if args.MaxLines != nil && *args.MaxLines < 0 {
		return errorResult("max_lines must be non-negative")
	}
	if args.MaxBytes != nil && *args.MaxBytes < 0 {
		return errorResult("max_bytes must be non-negative")
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

	// Validate the intentionally small direct-command surface.
	if !s.config.IsAllowed(basename) || !s.config.IsSubcommandAllowed(basename, parts[1:]) {
		event := runMetricsEvent(args, basename, parts)
		event.Allowed = false
		event.RejectionReason = runRejectionReason(s.config, basename, parts[1:])
		s.metrics.Record(event)
		return deniedRunResult(s.config, basename, parts, event.RejectionReason)
	}

	// Convert filter args to internal Filter type.
	var filters []Filter
	for _, f := range args.Filters {
		ft, err := parseFilterType(f.Type)
		if err != nil {
			event := runMetricsEvent(args, basename, parts)
			event.Allowed = true
			event.RejectionReason = "invalid_filter"
			s.metrics.Record(event)
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

	// Determine final response limits after filters.
	maxLines := s.config.MaxOutputFor(basename)
	if args.MaxLines != nil {
		maxLines = *args.MaxLines
	}
	maxBytes := s.config.Defaults.MaxOutputBytes
	if args.MaxBytes != nil {
		maxBytes = *args.MaxBytes
	}

	// Execute with timing.
	start := time.Now()
	result, err := ExecuteWithLimits(ctx, s.config, parsed, timeoutSec, maxLines, maxBytes, s.config.Defaults.MaxStreamBytes, args.Cwd)
	durationMs := time.Since(start).Milliseconds()

	if err != nil {
		event := runMetricsEvent(args, basename, parts)
		event.Allowed = true
		event.RejectionReason = "execution_error"
		event.DurationMs = durationMs
		s.metrics.Record(event)
		return errorResult(err.Error())
	}

	// Count output lines for metrics.
	outputLines := countLines(result.Stdout)
	exitCode := result.ExitCode

	event := runMetricsEvent(args, basename, parts)
	event.Allowed = true
	event.ExitCode = &exitCode
	event.DurationMs = durationMs
	event.OutputLinesPre = outputLines
	event.OutputLinesPost = outputLines // same if no filters changed line count at this level
	event.Truncated = result.Truncated
	event.StderrTruncated = result.StderrTruncated
	s.metrics.Record(event)

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

func (s *Server) handleFind(ctx context.Context, params json.RawMessage) *ToolResult {
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

	// Run find with timing and the server's bounded result collection.
	start := time.Now()
	search, err := RunFindWithLimits(ctx, fp, s.config.Defaults.MaxFindResults)
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
	resultCount := len(search.Results)
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
		Tool:          "find",
		Path:          args.Path,
		FindParams:    findParams,
		Allowed:       true,
		DurationMs:    durationMs,
		ResultCount:   &resultCount,
		FindTruncated: search.Truncated,
	})

	return textResult(formatFindResults(search, s.config.Defaults.MaxOutputBytes))
}

func formatFindResults(search FindSearchResult, maxBytes int) string {
	if len(search.Results) == 0 {
		return "no matches found"
	}

	var b strings.Builder
	shown := 0
	truncated := search.Truncated
	for _, result := range search.Results {
		line := result.Path + "\n"
		switch result.Type {
		case "dir":
			line = result.Path + "/\n"
		case "symlink":
			line = result.Path + "@\n"
		}
		if maxBytes > 0 && b.Len()+len(line) > maxBytes {
			truncated = true
			break
		}
		b.WriteString(line)
		shown++
	}
	if truncated {
		fmt.Fprintf(&b, "\n--- %d matches shown; results truncated\n", shown)
	} else {
		fmt.Fprintf(&b, "\n--- %d matches\n", shown)
	}
	return b.String()
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
		meta = append(meta, "truncated=true (final output reached max_lines or max_bytes)")
	}
	if r.StderrTruncated {
		meta = append(meta, "stderr_truncated=true (stderr reached max_stream_bytes)")
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
