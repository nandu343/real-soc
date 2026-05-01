// Package mcp implements a Model Context Protocol (MCP) compatible tool host.
//
// MCP overview
// ────────────
// The Model Context Protocol defines a standardised JSON-RPC 2.0 interface
// through which AI agents can discover and invoke local "tools" (e.g. YARA
// scanners, sandbox utilities, network probes) without baking those
// integrations into the agent binary itself.
//
// This package provides:
//
//   - ToolRegistry  — a compile-time and runtime registry of available tools.
//   - MCPServer     — an HTTP endpoint that exposes tools/list and tools/call
//     over JSON-RPC 2.0, following the MCP spec.
//   - Built-in tool stubs for common SOC utilities (YARA, file hash, sandbox).
//
// Agents running inside Docker containers communicate with this host server
// via the DOCKER_HOST_GATEWAY special hostname (172.17.0.1 by default) on
// the port defined by MCP_PORT (default 8081).
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"

	"github.com/gofiber/fiber/v2"

	memdump "github.com/nandu343/real-soc/internal/tools/memdump"
	yaraengine "github.com/nandu343/real-soc/internal/tools/yara"
)

// --------------------------------------------------------------------------
// Tool primitives
// --------------------------------------------------------------------------

// ToolParam describes a single parameter accepted by a Tool.
type ToolParam struct {
	// Name is the parameter key.
	Name string `json:"name"`
	// Type is a JSON Schema primitive type: "string", "number", "boolean", "object".
	Type string `json:"type"`
	// Description is a human-readable explanation of the parameter.
	Description string `json:"description"`
	// Required indicates whether the parameter must be provided.
	Required bool `json:"required"`
}

// Tool is a callable utility exposed to agents through the MCP interface.
type Tool struct {
	// Name is the stable identifier used in tools/call requests.
	Name string `json:"name"`
	// Description helps agents understand what the tool does.
	Description string `json:"description"`
	// Params lists the accepted input parameters.
	Params []ToolParam `json:"params"`
	// Handler is the Go function invoked when the tool is called.
	// It receives the raw JSON params object and returns a result or error.
	Handler func(ctx context.Context, params map[string]any) (any, error) `json:"-"`
}

// --------------------------------------------------------------------------
// ToolRegistry
// --------------------------------------------------------------------------

// ToolRegistry holds all registered MCP tools and provides thread-safe
// lookup.  Tools are registered once at startup and never removed.
type ToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]*Tool
}

// NewToolRegistry returns an empty registry.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{tools: make(map[string]*Tool)}
}

// Register adds a tool to the registry.  Panics if a tool with the same name
// is registered twice (this is a programming error, not a runtime one).
func (r *ToolRegistry) Register(t *Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[t.Name]; exists {
		panic(fmt.Sprintf("mcp: duplicate tool registration: %s", t.Name))
	}
	r.tools[t.Name] = t
}

// List returns all registered tools ordered by name.
func (r *ToolRegistry) List() []*Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tools := make([]*Tool, 0, len(r.tools))
	for _, t := range r.tools {
		tools = append(tools, t)
	}
	return tools
}

// Get returns the tool with the given name, or nil if not found.
func (r *ToolRegistry) Get(name string) *Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[name]
}

// --------------------------------------------------------------------------
// JSON-RPC 2.0 types
// --------------------------------------------------------------------------

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// --------------------------------------------------------------------------
// MCPServer
// --------------------------------------------------------------------------

// MCPServer exposes the ToolRegistry over HTTP using the MCP JSON-RPC 2.0
// protocol.  It is mounted on the main Fiber application under /mcp.
type MCPServer struct {
	registry *ToolRegistry
}

// NewMCPServer creates an MCPServer backed by the given ToolRegistry.
func NewMCPServer(registry *ToolRegistry) *MCPServer {
	return &MCPServer{registry: registry}
}

// Mount registers the MCP routes on the provided Fiber router.
//
// Routes:
//
//	POST /mcp/rpc   — JSON-RPC 2.0 endpoint (tools/list, tools/call)
//	GET  /mcp/tools — convenience REST endpoint listing available tools
func (s *MCPServer) Mount(router fiber.Router) {
	router.Post("/mcp/rpc", s.handleRPC)
	router.Get("/mcp/tools", s.handleListTools)
}

// handleRPC dispatches a JSON-RPC 2.0 request to the appropriate method.
func (s *MCPServer) handleRPC(c *fiber.Ctx) error {
	var req rpcRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(http.StatusBadRequest).JSON(rpcResponse{
			JSONRPC: "2.0",
			Error:   &rpcError{Code: -32700, Message: "parse error"},
		})
	}

	switch req.Method {
	case "tools/list":
		return s.rpcListTools(c, req)
	case "tools/call":
		return s.rpcCallTool(c, req)
	default:
		return c.Status(http.StatusOK).JSON(rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32601, Message: "method not found"},
		})
	}
}

// rpcListTools handles the tools/list JSON-RPC method.
func (s *MCPServer) rpcListTools(c *fiber.Ctx, req rpcRequest) error {
	tools := s.registry.List()
	// Strip the Handler func from the response (it can't be serialised).
	type toolDescriptor struct {
		Name        string      `json:"name"`
		Description string      `json:"description"`
		Params      []ToolParam `json:"params"`
	}
	result := make([]toolDescriptor, 0, len(tools))
	for _, t := range tools {
		result = append(result, toolDescriptor{
			Name:        t.Name,
			Description: t.Description,
			Params:      t.Params,
		})
	}
	return c.JSON(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result})
}

// rpcCallTool handles the tools/call JSON-RPC method.
func (s *MCPServer) rpcCallTool(c *fiber.Ctx, req rpcRequest) error {
	var callParams struct {
		Name   string         `json:"name"`
		Params map[string]any `json:"params"`
	}
	if err := json.Unmarshal(req.Params, &callParams); err != nil {
		return c.JSON(rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32602, Message: "invalid params"},
		})
	}

	tool := s.registry.Get(callParams.Name)
	if tool == nil {
		return c.JSON(rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32601, Message: "tool not found: " + callParams.Name},
		})
	}

	result, err := tool.Handler(c.Context(), callParams.Params)
	if err != nil {
		return c.JSON(rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32000, Message: err.Error()},
		})
	}

	return c.JSON(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result})
}

// handleListTools is a convenience REST endpoint that returns the tool list
// as plain JSON (not wrapped in JSON-RPC).
func (s *MCPServer) handleListTools(c *fiber.Ctx) error {
	return c.JSON(s.registry.List())
}

// --------------------------------------------------------------------------
// Built-in SOC tool stubs
// --------------------------------------------------------------------------

// RegisterBuiltinTools populates the registry with stub implementations of
// common SOC utilities.  In production, replace the stub bodies with real
// integrations (e.g. exec YARA, call a sandbox API, run a file-hash lookup).
func RegisterBuiltinTools(r *ToolRegistry) {
	r.Register(&Tool{
		Name:        "yara_scan",
		Description: "Scan a file or memory buffer against a set of YARA rules.",
		Params: []ToolParam{
			{Name: "target_path", Type: "string", Description: "Absolute path of the file to scan.", Required: true},
			{Name: "rules_path", Type: "string", Description: "Path to the .yar rules file or directory.", Required: false},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			tp, ok := params["target_path"].(string)
			if !ok || tp == "" {
				return nil, fmt.Errorf("missing or invalid target_path")
			}
			rp := ""
			if v, ok := params["rules_path"]; ok {
				if s, ok := v.(string); ok {
					rp = s
				}
			}

			eng := yaraengine.NewEngine()
			// If rules_path is omitted, look for a local `rules/` directory.
			if rp == "" {
				if _, err := os.Stat("rules"); err == nil {
					rp = "rules"
				}
			}

			if rp != "" {
				if err := eng.CompileRulesFromFile(rp); err != nil {
					return nil, err
				}
			}

			res, err := eng.ScanFile(ctx, tp)
			if err != nil {
				return nil, err
			}

			matched := make([]string, 0, len(res.Matches))
			for _, m := range res.Matches {
				matched = append(matched, m.Rule)
			}

			return map[string]any{
				"matched_rules": matched,
				"matches":       res.Matches,
				"scanned_bytes": res.ScannedBytes,
				"status":        res.Status,
			}, nil
		},
	})

	r.Register(&Tool{
		Name:        "mem_strings",
		Description: "Extract printable strings from a memory dump file.",
		Params: []ToolParam{
			{Name: "target_path", Type: "string", Description: "Absolute path of the memory dump file.", Required: true},
			{Name: "min_len", Type: "number", Description: "Minimum string length (default 4).", Required: false},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			tp, ok := params["target_path"].(string)
			if !ok || tp == "" {
				return nil, fmt.Errorf("missing or invalid target_path")
			}
			minLen := 4
			if v, ok := params["min_len"]; ok {
				switch n := v.(type) {
				case float64:
					minLen = int(n)
				case int:
					minLen = n
				case int64:
					minLen = int(n)
				case string:
					if parsed, err := strconv.Atoi(n); err == nil {
						minLen = parsed
					}
				}
			}

			strs, err := memdump.ExtractStringsFromFile(tp, minLen)
			if err != nil {
				return nil, err
			}

			return map[string]any{
				"count":   len(strs),
				"strings": strs,
				"status":  "ok",
			}, nil
		},
	})

	r.Register(&Tool{
		Name:        "file_hash",
		Description: "Compute SHA-256 and MD5 hashes of a local file.",
		Params: []ToolParam{
			{Name: "path", Type: "string", Description: "Absolute path of the file to hash.", Required: true},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			// TODO: compute real hashes.
			return map[string]any{
				"sha256": "stub",
				"md5":    "stub",
				"status": "stub — replace with real hash computation",
			}, nil
		},
	})

	r.Register(&Tool{
		Name:        "sandbox_detonate",
		Description: "Submit a file or URL to a malware sandbox for dynamic analysis.",
		Params: []ToolParam{
			{Name: "target", Type: "string", Description: "File path or URL to submit.", Required: true},
			{Name: "timeout_seconds", Type: "number", Description: "Maximum analysis time.", Required: false},
		},
		Handler: func(ctx context.Context, params map[string]any) (any, error) {
			// TODO: integrate with Cuckoo/ANY.RUN/Cape sandbox API.
			return map[string]any{
				"report_id": "stub",
				"verdict":   "unknown",
				"status":    "stub — replace with real sandbox integration",
			}, nil
		},
	})
}
