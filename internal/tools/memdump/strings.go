package memdump

import (
	"io"
	"os"
)

// ExtractStringsFromFile scans a file and returns printable ASCII strings of
// length >= minLen. It behaves similarly to the Unix `strings` utility but
// is intentionally small and dependency-free so it can run inside the
// orchestrator's environment.
func ExtractStringsFromFile(path string, minLen int) ([]string, error) {
	if minLen <= 0 {
		minLen = 4
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	const bufSize = 32 * 1024
	buf := make([]byte, bufSize)

	var out []string
	var cur []byte

	for {
		n, err := f.Read(buf)
		if n > 0 {
			for i := 0; i < n; i++ {
				b := buf[i]
				// Printable ASCII range (space .. tilde)
				if b >= 0x20 && b <= 0x7E {
					cur = append(cur, b)
					continue
				}
				if len(cur) >= minLen {
					out = append(out, string(cur))
				}
				cur = cur[:0]
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}

	if len(cur) >= minLen {
		out = append(out, string(cur))
	}

	return out, nil
}
