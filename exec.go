package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// RunResult is the structured output returned to the model.
type RunResult struct {
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr,omitempty"`
	ExitCode  int    `json:"exit_code"`
	Filtered  bool   `json:"filtered,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// Execute runs a parsed command through the whitelist, streams output
// through filters, and returns a structured result.
func Execute(ctx context.Context, cfg *Config, p *Parsed, timeoutSec int, maxLines int, cwd string) (*RunResult, error) {
	// Validate against whitelist.
	basename := filepath.Base(p.Command)
	if !cfg.IsAllowed(basename) {
		return nil, fmt.Errorf("command %q is not in the whitelist", basename)
	}

	// Validate subcommand if restricted.
	if !cfg.IsSubcommandAllowed(basename, p.Args) {
		allowed := cfg.Commands[basename].Subcommands
		return nil, fmt.Errorf("subcommand %q is not allowed for %q (allowed: %s)",
			firstArg(p.Args), basename, strings.Join(allowed, ", "))
	}

	// Resolve binary path.
	binPath, err := cfg.Resolve(basename)
	if err != nil {
		return nil, err
	}

	// Build timeout context.
	if timeoutSec > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
		defer cancel()
	}

	// Build command.
	cmd := exec.CommandContext(ctx, binPath, p.Args...)
	if cwd != "" {
		cmd.Dir = cwd
	}

	// Environment: inherit current env, add config env, add request env.
	env := os.Environ()
	if cfgEnv := cfg.EnvFor(basename); cfgEnv != nil {
		for k, v := range cfgEnv {
			env = append(env, k+"="+v)
		}
	}
	for k, v := range p.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env

	// Capture stderr separately.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	// Set up stdout pipeline.
	var output string
	filtered := len(p.Filters) > 0
	truncated := false

	if filtered {
		// Stream stdout through the filter chain.
		stdoutPipe, err := cmd.StdoutPipe()
		if err != nil {
			return nil, fmt.Errorf("stdout pipe: %w", err)
		}

		if err := cmd.Start(); err != nil {
			return nil, fmt.Errorf("start: %w", err)
		}

		// Build the reader pipeline: stdout → filters.
		// When filters are present, skip max-lines truncation — the filters
		// themselves are the narrowing mechanism. Pre-truncation breaks
		// structured filters like jq that need complete input.
		var reader io.Reader = stdoutPipe

		// Apply filter chain.
		output, err = ApplyFilterStream(reader, p.Filters)
		if err != nil {
			// Drain any remaining stdout to avoid blocking the process.
			io.Copy(io.Discard, stdoutPipe)
			cmd.Wait()
			return &RunResult{
				Stdout:   "",
				Stderr:   fmt.Sprintf("filter error: %v", err),
				ExitCode: -1,
			}, nil
		}

		// Wait for process to finish.
		err = cmd.Wait()
	} else {
		// No filters — capture stdout directly.
		var stdout bytes.Buffer
		cmd.Stdout = &stdout

		err = cmd.Run()
		output = stdout.String()
		if err == nil && maxLines > 0 {
			lines := strings.Split(output, "\n")
			if len(lines) > maxLines {
				output = strings.Join(lines[:maxLines], "\n")
				truncated = true
			}
		}
	}

	exitCode := 0
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return &RunResult{
				Stdout:   output,
				Stderr:   fmt.Sprintf("command timed out after %d seconds", timeoutSec),
				ExitCode: -1,
			}, nil
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("exec: %w", err)
		}
	}

	return &RunResult{
		Stdout:    output,
		Stderr:    stderr.String(),
		ExitCode:  exitCode,
		Filtered:  filtered,
		Truncated: truncated,
	}, nil
}

// maxLinesReader reads up to N lines from the underlying reader, then
// returns EOF. Sets *truncated to true if lines were discarded.
type maxLinesReader struct {
	scanner   *bufio.Scanner
	remaining int
	truncated *bool
	buf       bytes.Buffer
	done      bool
}

func newMaxLinesReader(r io.Reader, maxLines int, truncated *bool) io.Reader {
	return &maxLinesReader{
		scanner:   bufio.NewScanner(r),
		remaining: maxLines,
		truncated: truncated,
	}
}

func (m *maxLinesReader) Read(p []byte) (int, error) {
	// If we have buffered data from a previous scan, serve it first.
	if m.buf.Len() > 0 {
		return m.buf.Read(p)
	}
	if m.done {
		return 0, io.EOF
	}

	// Scan the next line.
	if !m.scanner.Scan() {
		m.done = true
		if err := m.scanner.Err(); err != nil {
			return 0, err
		}
		return 0, io.EOF
	}

	if m.remaining <= 0 {
		// We've hit the limit. Mark truncated and stop.
		*m.truncated = true
		m.done = true
		return 0, io.EOF
	}

	m.remaining--
	m.buf.WriteString(m.scanner.Text())
	m.buf.WriteByte('\n')
	return m.buf.Read(p)
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	// Skip flags to find the subcommand, matching IsSubcommandAllowed logic.
	for _, a := range args {
		if len(a) > 0 && a[0] != '-' {
			return a
		}
	}
	// All args are flags — return the first one for the error message.
	return args[0]
}
