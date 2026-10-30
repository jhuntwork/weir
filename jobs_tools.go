package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

func objectSchema(properties map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "object", "properties": properties, "required": required}
}

const (
	defaultJobListLimit = 50
	maxJobListLimit     = 100
	defaultJobLogBytes  = 32 * 1024
	maxJobLogBytes      = 64 * 1024
	defaultJobLogLines  = 1000
	maxJobLogLines      = 5000
)

type jobMetadata struct {
	ID             string     `json:"id"`
	State          JobState   `json:"state"`
	Command        string     `json:"command"`
	Argv           []string   `json:"argv"`
	Cwd            string     `json:"cwd,omitempty"`
	Env            []string   `json:"env,omitempty"`
	TimeoutSeconds int        `json:"timeout_seconds,omitempty"`
	Attribution    string     `json:"attribution,omitempty"`
	EffectScope    string     `json:"effect_scope"`
	CreatedAt      time.Time  `json:"created_at"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	DurationMs     int64      `json:"duration_ms,omitempty"`
	PID            int        `json:"pid,omitempty"`
	PGID           int        `json:"pgid,omitempty"`
	ExitCode       *int       `json:"exit_code,omitempty"`
	TerminalError  string     `json:"terminal_error,omitempty"`
	StdoutCut      bool       `json:"stdout_truncated,omitempty"`
	StderrCut      bool       `json:"stderr_truncated,omitempty"`
}

func metadataFor(job *Job) *jobMetadata {
	return &jobMetadata{
		ID:             job.ID,
		State:          job.State,
		Command:        job.Command,
		Argv:           append([]string(nil), job.Argv...),
		Cwd:            job.Cwd,
		Env:            append([]string(nil), job.Env...),
		TimeoutSeconds: job.TimeoutSeconds,
		Attribution:    job.Attribution,
		EffectScope:    job.EffectScope,
		CreatedAt:      job.CreatedAt,
		StartedAt:      job.StartedAt,
		FinishedAt:     job.FinishedAt,
		DurationMs:     job.DurationMs,
		PID:            job.PID,
		PGID:           job.PGID,
		ExitCode:       job.ExitCode,
		TerminalError:  job.TerminalError,
		StdoutCut:      job.StdoutCut,
		StderrCut:      job.StderrCut,
	}
}

func metadataText(job *Job) *ToolResult {
	return jobText(metadataFor(job))
}

func boundJobLog(value string, maxBytes, maxLines int) (string, bool) {
	truncated := false
	if countLines(value) > maxLines {
		end := 0
		for lines := 0; lines < maxLines; lines++ {
			relative := strings.IndexByte(value[end:], byte(10))
			if relative < 0 {
				break
			}
			end += relative + 1
		}
		value = value[:end]
		truncated = true
	}
	if len(value) > maxBytes {
		value = value[:maxBytes]
		truncated = true
	}
	return value, truncated
}

func (s *Server) registerJobTools() {
	s.register(Tool{Name: "job_start", Description: "Start an inspectable server-lifetime local job using Weir's existing restricted command surface. Returns an opaque job ID; this is not a detached shell or a remote-work guarantee.", InputSchema: objectSchema(map[string]any{
		"command": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "env": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}, "timeout": map[string]any{"type": "integer", "minimum": 0}, "attribution": map[string]any{"type": "string"},
	}, "command")}, s.handleJobStart)
	s.register(Tool{Name: "job_get", Description: "Get one job's observable lifecycle state, outcome, and bounded captured metadata.", InputSchema: objectSchema(map[string]any{"id": map[string]any{"type": "string"}}, "id")}, s.handleJobGet)
	s.register(Tool{Name: "job_wait", Description: "Wait up to a bounded number of seconds for one job to become terminal, then return its current metadata. A wait timeout never cancels the job.", InputSchema: objectSchema(map[string]any{
		"id": map[string]any{"type": "string"}, "timeout": map[string]any{"type": "integer", "minimum": 1, "maximum": 300},
	}, "id", "timeout")}, s.handleJobWait)
	s.register(Tool{Name: "job_list", Description: "List a bounded page of inspectable local job metadata in newest-first order.", InputSchema: objectSchema(map[string]any{
		"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": maxJobListLimit},
		"offset": map[string]any{"type": "integer", "minimum": 0},
	})}, s.handleJobList)
	s.register(Tool{Name: "job_logs", Description: "Read explicitly bounded captured stdout and stderr for one job, with capture and response truncation flags.", InputSchema: objectSchema(map[string]any{
		"id":        map[string]any{"type": "string"},
		"max_bytes": map[string]any{"type": "integer", "minimum": 1, "maximum": maxJobLogBytes},
		"max_lines": map[string]any{"type": "integer", "minimum": 1, "maximum": maxJobLogLines},
	}, "id")}, s.handleJobLogs)
	s.register(Tool{Name: "job_cancel", Description: "Cancel a Weir-owned local process tree and wait until its direct child is reaped.", InputSchema: objectSchema(map[string]any{"id": map[string]any{"type": "string"}}, "id")}, s.handleJobCancel)
	s.register(Tool{Name: "job_cleanup", Description: "Mark terminal jobs past the configured retention as expired and clear their bounded logs.", InputSchema: objectSchema(map[string]any{})}, s.handleJobCleanup)
}

type jobStartArgs struct {
	Command, Cwd, Attribution string
	Env                       map[string]string
	Timeout                   int
}

func decodeJobID(params json.RawMessage) (string, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return "", err
	}
	if p.ID == "" {
		return "", fmt.Errorf("id is required")
	}
	return p.ID, nil
}
func jobText(value any) *ToolResult {
	data, err := json.Marshal(value)
	if err != nil {
		return errorResult(err.Error())
	}
	return textResult(string(data))
}
func (s *Server) handleJobStart(_ context.Context, params json.RawMessage) *ToolResult {
	var p jobStartArgs
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResult("parse params: " + err.Error())
	}
	if p.Command == "" {
		return errorResult("command is required")
	}
	j, err := s.jobs.Start(p.Command, p.Cwd, p.Env, p.Timeout, p.Attribution)
	if err != nil {
		return errorResult(err.Error())
	}
	return metadataText(j)
}
func (s *Server) handleJobGet(_ context.Context, params json.RawMessage) *ToolResult {
	id, err := decodeJobID(params)
	if err != nil {
		return errorResult(err.Error())
	}
	j, err := s.jobs.Get(id)
	if err != nil {
		return errorResult("job not found")
	}
	return metadataText(j)
}
func (s *Server) handleJobWait(ctx context.Context, params json.RawMessage) *ToolResult {
	var p struct {
		ID      string `json:"id"`
		Timeout int    `json:"timeout"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResult("parse params: " + err.Error())
	}
	if p.ID == "" {
		return errorResult("id is required")
	}
	if p.Timeout < 1 || p.Timeout > 300 {
		return errorResult("timeout must be between 1 and 300 seconds")
	}
	j, err := s.jobs.Wait(ctx, p.ID, time.Duration(p.Timeout)*time.Second)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errorResult("job not found")
		}
		return errorResult(err.Error())
	}
	return metadataText(j)
}
func (s *Server) handleJobList(_ context.Context, params json.RawMessage) *ToolResult {
	var p struct {
		Limit  *int `json:"limit,omitempty"`
		Offset int  `json:"offset,omitempty"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResult("parse params: " + err.Error())
	}
	if p.Offset < 0 {
		return errorResult("offset must be non-negative")
	}
	limit := defaultJobListLimit
	if p.Limit != nil {
		if *p.Limit < 1 || *p.Limit > maxJobListLimit {
			return errorResult(fmt.Sprintf("limit must be between 1 and %d", maxJobListLimit))
		}
		limit = *p.Limit
	}
	jobs, hasMore := s.jobs.ListPage(limit, p.Offset)
	metadata := make([]*jobMetadata, len(jobs))
	for i, job := range jobs {
		metadata[i] = metadataFor(job)
	}
	return jobText(map[string]any{
		"jobs":     metadata,
		"limit":    limit,
		"offset":   p.Offset,
		"has_more": hasMore,
	})
}
func (s *Server) handleJobLogs(_ context.Context, params json.RawMessage) *ToolResult {
	var p struct {
		ID       string `json:"id"`
		MaxBytes *int   `json:"max_bytes,omitempty"`
		MaxLines *int   `json:"max_lines,omitempty"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResult("parse params: " + err.Error())
	}
	if p.ID == "" {
		return errorResult("id is required")
	}
	maxBytes := defaultJobLogBytes
	if p.MaxBytes != nil {
		if *p.MaxBytes < 1 || *p.MaxBytes > maxJobLogBytes {
			return errorResult(fmt.Sprintf("max_bytes must be between 1 and %d", maxJobLogBytes))
		}
		maxBytes = *p.MaxBytes
	}
	maxLines := defaultJobLogLines
	if p.MaxLines != nil {
		if *p.MaxLines < 1 || *p.MaxLines > maxJobLogLines {
			return errorResult(fmt.Sprintf("max_lines must be between 1 and %d", maxJobLogLines))
		}
		maxLines = *p.MaxLines
	}
	j, err := s.jobs.Get(p.ID)
	if err != nil {
		return errorResult("job not found")
	}
	stdout, stdoutResponseCut := boundJobLog(j.Stdout, maxBytes, maxLines)
	stderr, stderrResponseCut := boundJobLog(j.Stderr, maxBytes, maxLines)
	return jobText(map[string]any{
		"id":                        j.ID,
		"state":                     j.State,
		"stdout":                    stdout,
		"stderr":                    stderr,
		"stdout_truncated":          j.StdoutCut,
		"stderr_truncated":          j.StderrCut,
		"stdout_response_truncated": stdoutResponseCut,
		"stderr_response_truncated": stderrResponseCut,
		"max_bytes":                 maxBytes,
		"max_lines":                 maxLines,
	})
}
func (s *Server) handleJobCancel(_ context.Context, params json.RawMessage) *ToolResult {
	id, err := decodeJobID(params)
	if err != nil {
		return errorResult(err.Error())
	}
	j, err := s.jobs.Cancel(id)
	if err != nil {
		return errorResult("job not found")
	}
	return metadataText(j)
}
func (s *Server) handleJobCleanup(_ context.Context, _ json.RawMessage) *ToolResult {
	n, err := s.jobs.Cleanup()
	if err != nil {
		return errorResult(err.Error())
	}
	return jobText(map[string]any{"expired": n})
}
