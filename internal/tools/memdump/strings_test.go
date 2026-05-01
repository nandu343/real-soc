package memdump

import (
	"os"
	"testing"
)

func TestExtractStringsFromFile(t *testing.T) {
	tmp, err := os.CreateTemp("", "memdump-test-*.bin")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	// Build a payload with embedded printable ASCII strings separated by nulls
	payload := []byte{0x00}
	payload = append(payload, []byte("abc")...)
	payload = append(payload, 0x00)
	payload = append(payload, []byte("hello world")...)
	payload = append(payload, 0x00)
	payload = append(payload, []byte("hi")...)
	payload = append(payload, 0x00)
	payload = append(payload, []byte("1234")...)

	if _, err := tmp.Write(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	res, err := ExtractStringsFromFile(tmp.Name(), 3)
	if err != nil {
		t.Fatalf("ExtractStringsFromFile: %v", err)
	}

	expected := map[string]bool{"abc": true, "hello world": true, "1234": true}
	for _, s := range res {
		delete(expected, s)
	}
	if len(expected) != 0 {
		t.Fatalf("missing expected strings: %v", expected)
	}
}
