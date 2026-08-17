package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// RunResult is the structured output returned to the model.
type RunResult struct {
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr,omitempty"`
	ExitCode        int    `json:"exit_code"`
	Filtered        bool   `json:"filtered,omitempty"`
	Truncated       bool   `json:"truncated,omitempty"`
	StderrTruncated bool   `json:"stderr_truncated,omitempty"`
}

// limitedBuffer captures at most limit bytes while continuing to accept writes
// so a verbose child process cannot grow stderr in memory without bound.
type limitedBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		return b.buffer.Write(p)
	}
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = b.buffer.Write(p[:remaining])
		b.truncated = true
		return len(p), nil
	}
	_, err := b.buffer.Write(p)
	return len(p), err
}

// Execute runs a parsed command through the whitelist. It preserves the
// historic API for callers that use Config defaults directly.
func Execute(ctx context.Context, cfg *Config, p *Parsed, timeoutSec int, maxLines int, cwd string) (*RunResult, error) {
	return ExecuteWithLimits(ctx, cfg, p, timeoutSec, maxLines, cfg.Defaults.MaxOutputBytes, cfg.Defaults.MaxStreamBytes, cwd)
}

// ExecuteWithLimits runs a command and applies response limits after its filter
// pipeline. MaxStreamBytes bounds every pipeline edge so buffered filters never
// accumulate unbounded input or output in memory.
func ExecuteWithLimits(ctx context.Context, cfg *Config, p *Parsed, timeoutSec, maxLines, maxBytes, maxStreamBytes int, cwd string) (*RunResult, error) {
	basename := filepath.Base(p.Command)
	if !cfg.IsAllowed(basename) {
		return nil, fmt.Errorf("command %q is not in the whitelist", basename)
	}
	if !cfg.IsSubcommandAllowed(basename, p.Args) {
		return nil, fmt.Errorf("subcommand %q is not allowed for %q (allowed: %s)",
			firstArg(p.Args), basename, strings.Join(cfg.Commands[basename].Subcommands, ", "))
	}

	binPath, err := cfg.Resolve(basename)
	if err != nil {
		return nil, err
	}

	if timeoutSec > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, binPath, p.Args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	configureProcessTree(cmd)

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

	stderr := &limitedBuffer{limit: maxStreamBytes}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	var output string
	var truncated bool
	if len(p.Filters) > 0 {
		output, truncated, err = ApplyFilterStreamWithLimits(stdout, p.Filters, maxLines, maxBytes, maxStreamBytes)
	} else {
		output, truncated, err = CollectOutput(newByteLimitReader(stdout, maxStreamBytes), maxLines, maxBytes)
	}
	if err != nil {
		// Once a stream budget is exceeded, stop the child rather than leaving a
		// writer blocked on stdout while this request holds the server loop.
		_ = terminateProcessTree(cmd)
		_ = cmd.Wait()
		if errors.Is(err, ErrStreamLimit) {
			return nil, fmt.Errorf("command output exceeded the %d-byte stream budget; narrow the command or filter earlier", maxStreamBytes)
		}
		return nil, fmt.Errorf("process output: %w", err)
	}

	err = cmd.Wait()
	exitCode := 0
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return &RunResult{
				Stdout:          output,
				Stderr:          fmt.Sprintf("command timed out after %d seconds", timeoutSec),
				ExitCode:        -1,
				Filtered:        len(p.Filters) > 0,
				Truncated:       truncated,
				StderrTruncated: stderr.truncated,
			}, nil
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("exec: %w", err)
		}
	}

	return &RunResult{
		Stdout:          output,
		Stderr:          stderr.buffer.String(),
		ExitCode:        exitCode,
		Filtered:        len(p.Filters) > 0,
		Truncated:       truncated,
		StderrTruncated: stderr.truncated,
	}, nil
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}
