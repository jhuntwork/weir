package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBoundedResponseBytesReplacesOversizedResponse(t *testing.T) {
	response := response{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Result:  textResult(strings.Repeat("x", maxSerializedResponseBytes)),
	}
	data := boundedResponseBytes(response)
	if len(data)+1 > maxSerializedResponseBytes {
		t.Fatalf("serialized response is too large: %d bytes", len(data)+1)
	}
	if strings.Contains(string(data), strings.Repeat("x", 1024)) {
		t.Fatal("oversized result was not replaced")
	}
	if !strings.Contains(string(data), "exceeded the serialized response limit") {
		t.Fatalf("replacement did not explain the failure: %s", data)
	}
}

func TestBoundedResponseBytesPreservesSmallResponse(t *testing.T) {
	data := boundedResponseBytes(response{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Result:  textResult("small"),
	})
	if !strings.Contains(string(data), `"text":"small"`) {
		t.Fatalf("small response changed unexpectedly: %s", data)
	}
}
