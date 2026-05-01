package yara

import (
	"context"
	"os"
)

// Match represents a single YARA rule match.
type Match struct {
	Rule      string            `json:"rule"`
	Namespace string            `json:"namespace,omitempty"`
	Tags      []string          `json:"tags,omitempty"`
	Meta      map[string]string `json:"meta,omitempty"`
	Strings   []string          `json:"strings,omitempty"`
}

// ScanResult is the normalized result returned by the YARA engine.
type ScanResult struct {
	Matches      []Match `json:"matches"`
	ScannedBytes int64   `json:"scanned_bytes"`
	Status       string  `json:"status"`
}

// Engine is the minimal interface used by the MCP host to compile rules and
// scan files or memory buffers.  A production implementation should use the
// native libyara bindings (see real_yara.go) but this package provides a
// mock implementation by default so the repository builds on machines without
// libyara installed.
type Engine interface {
	// CompileRulesFromFile loads and compiles rules from a .yar file or
	// directory path. Implementations may cache compiled rules for repeated
	// scans.
	CompileRulesFromFile(path string) error

	// CompileRulesFromString compiles YARA rules provided as a string.
	CompileRulesFromString(content string) error

	// ScanFile scans a file at the given path and returns matches.
	ScanFile(ctx context.Context, path string) (ScanResult, error)

	// ScanBytes scans an in-memory byte slice.
	ScanBytes(ctx context.Context, data []byte) (ScanResult, error)
}

// NewEngine returns a new Engine implementation. By default this returns a
// lightweight mock that performs no real rule matching. To enable the real
// libyara-backed implementation, build the binary with `-tags=yara` (see
// README for instructions).
func NewEngine() Engine {
	return &mockEngine{}
}

// ---------------------------------------------------------------------------
// Mock implementation (default)
// ---------------------------------------------------------------------------

type mockEngine struct{}

func (m *mockEngine) CompileRulesFromFile(path string) error {
	// noop for mock
	_ = path
	return nil
}

func (m *mockEngine) CompileRulesFromString(content string) error {
	_ = content
	return nil
}

func (m *mockEngine) ScanFile(ctx context.Context, path string) (ScanResult, error) {
	var sz int64
	if fi, err := os.Stat(path); err == nil {
		sz = fi.Size()
	}
	return ScanResult{Matches: nil, ScannedBytes: sz, Status: "mock"}, nil
}

func (m *mockEngine) ScanBytes(ctx context.Context, data []byte) (ScanResult, error) {
	return ScanResult{Matches: nil, ScannedBytes: int64(len(data)), Status: "mock"}, nil
}
