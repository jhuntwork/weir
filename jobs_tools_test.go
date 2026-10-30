package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func jobParams(id string, extra string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"id":%q%s}`, id, extra))
}

func decodeToolJSON(t *testing.T, result *ToolResult) any {
	t.Helper()
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content[0].Text)
	}
	var value any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &value); err != nil {
		t.Fatalf("decode tool result: %v; text=%q", err, result.Content[0].Text)
	}
	return value
}

func hasJSONKey(value any, key string) bool {
	switch value := value.(type) {
	case map[string]any:
		if _, ok := value[key]; ok {
			return true
		}
		for _, child := range value {
			if hasJSONKey(child, key) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if hasJSONKey(child, key) {
				return true
			}
		}
	}
	return false
}

func TestLifecycleResponsesExcludeCapturedLogs(t *testing.T) {
	manager := testJobs(t, 1)
	job, err := manager.Start(`sh -c "printf secret; printf diagnostic >&2"`, "", nil, 0, "metadata")
	if err != nil {
		t.Fatal(err)
	}
	job = waitJob(t, manager, job.ID)
	server := &Server{jobs: manager}

	results := []*ToolResult{
		server.handleJobGet(context.Background(), jobParams(job.ID, "")),
		server.handleJobWait(context.Background(), jobParams(job.ID, `,"timeout":1`)),
		server.handleJobList(context.Background(), json.RawMessage(`{"limit":10}`)),
		server.handleJobCancel(context.Background(), jobParams(job.ID, "")),
	}
	for i, result := range results {
		value := decodeToolJSON(t, result)
		if hasJSONKey(value, "stdout") || hasJSONKey(value, "stderr") {
			t.Fatalf("lifecycle response %d exposed captured logs: %s", i, result.Content[0].Text)
		}
	}
}

func TestJobLogsApplyExplicitByteAndLineBounds(t *testing.T) {
	manager := testJobs(t, 1)
	job, err := manager.Start(`sh -c "printf 123456789; printf abcdefghij >&2"`, "", nil, 0, "logs")
	if err != nil {
		t.Fatal(err)
	}
	job = waitJob(t, manager, job.ID)
	server := &Server{jobs: manager}

	result := server.handleJobLogs(context.Background(), jobParams(job.ID, `,"max_bytes":5,"max_lines":1`))
	value := decodeToolJSON(t, result).(map[string]any)
	stdout := value["stdout"].(string)
	stderr := value["stderr"].(string)
	if len(stdout) > 5 || len(stderr) > 5 {
		t.Fatalf("log byte bound exceeded: stdout=%q stderr=%q", stdout, stderr)
	}
	if countLines(stdout) > 1 || countLines(stderr) > 1 {
		t.Fatalf("log line bound exceeded: stdout=%q stderr=%q", stdout, stderr)
	}
	if value["stdout_response_truncated"] != true || value["stderr_response_truncated"] != true {
		t.Fatalf("response truncation flags missing: %#v", value)
	}

	invalid := server.handleJobLogs(context.Background(), jobParams(job.ID, `,"max_bytes":0`))
	if !invalid.IsError || !strings.Contains(invalid.Content[0].Text, "max_bytes") {
		t.Fatalf("invalid log bound was accepted: %#v", invalid)
	}
}

func TestJobListReturnsBoundedPage(t *testing.T) {
	manager := testJobs(t, 1)
	for i := 0; i < 2; i++ {
		job, err := manager.Start(`sh -c "printf job"`, "", nil, 0, "page")
		if err != nil {
			t.Fatal(err)
		}
		waitJob(t, manager, job.ID)
	}
	server := &Server{jobs: manager}
	result := server.handleJobList(context.Background(), json.RawMessage(`{"limit":1,"offset":0}`))
	value := decodeToolJSON(t, result).(map[string]any)
	jobs := value["jobs"].([]any)
	if len(jobs) != 1 || value["has_more"] != true {
		t.Fatalf("unexpected bounded page: %#v", value)
	}
	if hasJSONKey(jobs[0], "stdout") || hasJSONKey(jobs[0], "stderr") {
		t.Fatalf("job page exposed captured logs: %#v", jobs[0])
	}
}
