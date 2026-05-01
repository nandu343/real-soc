//go:build yara
// +build yara

package yara

import (
	"context"
	"os"
	"testing"
)

// This integration test is built only when the `yara` build tag is enabled
// and `libyara` is available on the system. It verifies that the real
// engine can compile rules from a string and successfully match a file.
func TestRealEngineCompileAndScan(t *testing.T) {
	ctx := context.Background()

	rule := `rule contains_hello {
    strings:
        $s = "hello"
    condition:
        $s
}`

	eng := NewEngine()
	if err := eng.CompileRulesFromString(rule); err != nil {
		t.Fatalf("CompileRulesFromString: %v", err)
	}

	f, err := os.CreateTemp("", "yara-real-*.txt")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer os.Remove(f.Name())
	defer f.Close()

	if _, err := f.Write([]byte("hello world")); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	res, err := eng.ScanFile(ctx, f.Name())
	if err != nil {
		t.Fatalf("ScanFile: %v", err)
	}
	if len(res.Matches) == 0 {
		t.Fatalf("expected at least one match, got none")
	}
}
