package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// JSON-RPC 2.0 over stdio, per MCP spec.

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type initializeResult struct {
	ProtocolVersion string     `json:"protocolVersion"`
	Capabilities    any        `json:"capabilities"`
	ServerInfo      serverInfo `json:"serverInfo"`
}

// Tool is the public description sent to clients via tools/list.
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

// ToolResult is what MCP clients expect from tools/call.
type ToolResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolHandler func(ctx context.Context, params json.RawMessage) *ToolResult

// Server is an MCP server with registered tools.
type Server struct {
	config   *Config
	version  string
	tools    []Tool
	handlers map[string]toolHandler
}

func NewServer(cfg *Config, version string) *Server {
	s := &Server{
		config:   cfg,
		version:  version,
		handlers: make(map[string]toolHandler),
	}
	s.registerTools()
	return s
}

// Serve runs the JSON-RPC loop on stdin/stdout.
func (s *Server) Serve(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)
	const maxLineSize = 1 * 1024 * 1024
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, maxLineSize)

	enc := json.NewEncoder(os.Stdout)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			s.writeError(enc, nil, -32700, fmt.Sprintf("parse error: %v", err))
			continue
		}

		s.handle(ctx, enc, &req)
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return fmt.Errorf("scan: %w", err)
	}
	return nil
}

func (s *Server) handle(ctx context.Context, enc *json.Encoder, req *request) {
	switch req.Method {
	case "initialize":
		s.writeResult(enc, req.ID, initializeResult{
			ProtocolVersion: "2024-11-05",
			Capabilities:    map[string]any{"tools": map[string]any{}},
			ServerInfo:      serverInfo{Name: "weir", Version: s.version},
		})
	case "notifications/initialized":
		// No response required.
	case "tools/list":
		s.writeResult(enc, req.ID, map[string]any{"tools": s.tools})
	case "tools/call":
		s.handleToolCall(ctx, enc, req)
	default:
		if len(req.ID) > 0 && string(req.ID) != "null" {
			s.writeError(enc, req.ID, -32601, "method not found: "+req.Method)
		}
	}
}

func (s *Server) handleToolCall(ctx context.Context, enc *json.Encoder, req *request) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		s.writeError(enc, req.ID, -32602, "invalid params: "+err.Error())
		return
	}
	h, ok := s.handlers[p.Name]
	if !ok {
		s.writeError(enc, req.ID, -32601, "unknown tool: "+p.Name)
		return
	}
	result := h(ctx, p.Arguments)
	s.writeResult(enc, req.ID, result)
}

func (s *Server) writeResult(enc *json.Encoder, id json.RawMessage, result any) {
	if err := enc.Encode(response{JSONRPC: "2.0", ID: id, Result: result}); err != nil {
		fmt.Fprintln(os.Stderr, "weir: write result:", err)
	}
}

func (s *Server) writeError(enc *json.Encoder, id json.RawMessage, code int, msg string) {
	if err := enc.Encode(response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}); err != nil {
		fmt.Fprintln(os.Stderr, "weir: write error:", err)
	}
}

func (s *Server) register(t Tool, h toolHandler) {
	s.tools = append(s.tools, t)
	s.handlers[t.Name] = h
}

func textResult(text string) *ToolResult {
	return &ToolResult{Content: []ContentBlock{{Type: "text", Text: text}}}
}

func errorResult(msg string) *ToolResult {
	return &ToolResult{
		Content: []ContentBlock{{Type: "text", Text: msg}},
		IsError: true,
	}
}
