package yara

import (
	"context"
	"os"
	"testing"
)

func TestMockScanFileAndBytes(t *testing.T) {
	ctx := context.Background()

	// create a temporary file
	f, err := os.CreateTemp("", "yara-test-*.bin")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer os.Remove(f.Name())
	defer f.Close()

	payload := []byte("hello world")
	if _, err := f.Write(payload); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	eng := NewEngine()

	// ScanFile should return scanned bytes equal to file size for mock engine
	res, err := eng.ScanFile(ctx, f.Name())
	if err != nil {
		t.Fatalf("ScanFile error: %v", err)
	}
	if res.ScannedBytes != int64(len(payload)) {
		t.Fatalf("expected scanned bytes %d, got %d", len(payload), res.ScannedBytes)
	}

	// ScanBytes should report length
	res2, err := eng.ScanBytes(ctx, payload)
	if err != nil {
		t.Fatalf("ScanBytes error: %v", err)
	}
	if res2.ScannedBytes != int64(len(payload)) {
		t.Fatalf("expected scanned bytes %d, got %d", len(payload), res2.ScannedBytes)
	}
}
