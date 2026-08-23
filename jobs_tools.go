package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

func objectSchema(properties map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "object", "properties": properties, "required": required}
}

func (s *Server) registerJobTools() {
	s.register(Tool{Name: "job_start", Description: "Start an inspectable server-lifetime local job using Weir's existing restricted command surface. Returns an opaque job ID; this is not a detached shell or a remote-work guarantee.", InputSchema: objectSchema(map[string]any{
		"command": map[string]any{"type": "string"}, "cwd": map[string]any{"type": "string"}, "env": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}, "timeout": map[string]any{"type": "integer", "minimum": 0}, "attribution": map[string]any{"type": "string"},
	}, "command")}, s.handleJobStart)
	s.register(Tool{Name: "job_get", Description: "Get one job's observable lifecycle state, outcome, and bounded captured metadata.", InputSchema: objectSchema(map[string]any{"id": map[string]any{"type": "string"}}, "id")}, s.handleJobGet)
	s.register(Tool{Name: "job_wait", Description: "Wait up to a bounded number of seconds for one job to become terminal, then return its current snapshot. A wait timeout never cancels the job.", InputSchema: objectSchema(map[string]any{
		"id": map[string]any{"type": "string"}, "timeout": map[string]any{"type": "integer", "minimum": 1, "maximum": 300},
	}, "id", "timeout")}, s.handleJobWait)
	s.register(Tool{Name: "job_list", Description: "List inspectable local jobs in newest-first order.", InputSchema: objectSchema(map[string]any{})}, s.handleJobList)
	s.register(Tool{Name: "job_logs", Description: "Read bounded captured stdout and stderr for one job, with truncation flags.", InputSchema: objectSchema(map[string]any{"id": map[string]any{"type": "string"}}, "id")}, s.handleJobLogs)
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
	return jobText(j)
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
	return jobText(j)
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
	return jobText(j)
}
func (s *Server) handleJobList(_ context.Context, _ json.RawMessage) *ToolResult {
	return jobText(s.jobs.List())
}
func (s *Server) handleJobLogs(_ context.Context, params json.RawMessage) *ToolResult {
	id, err := decodeJobID(params)
	if err != nil {
		return errorResult(err.Error())
	}
	j, err := s.jobs.Get(id)
	if err != nil {
		return errorResult("job not found")
	}
	return jobText(map[string]any{"id": j.ID, "state": j.State, "stdout": j.Stdout, "stderr": j.Stderr, "stdout_truncated": j.StdoutCut, "stderr_truncated": j.StderrCut})
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
	return jobText(j)
}
func (s *Server) handleJobCleanup(_ context.Context, _ json.RawMessage) *ToolResult {
	n, err := s.jobs.Cleanup()
	if err != nil {
		return errorResult(err.Error())
	}
	return jobText(map[string]any{"expired": n})
}
