package transcript

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Mentions reports which of needles occur anywhere in the session's
// transcript. It streams the file, so a long transcript is never read
// into memory whole.
func (s Session) Mentions(needles []string) (map[string]bool, error) {
	found := map[string]bool{}
	longest := 0
	for _, n := range needles {
		longest = max(longest, len(n))
	}
	if longest == 0 {
		return found, nil
	}
	path := filepath.Join(s.Dir, "messages.jsonl")
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return found, nil
		}
		return nil, fmt.Errorf("io: transcript: open %s: %w", path, err)
	}
	defer f.Close()
	// Each window keeps the previous chunk's tail so a needle that
	// straddles two reads is still seen.
	chunk := make([]byte, 64*1024)
	var window []byte
	for {
		n, err := f.Read(chunk)
		if n > 0 {
			window = append(window, chunk[:n]...)
			for _, needle := range needles {
				if needle != "" && !found[needle] && bytes.Contains(window, []byte(needle)) {
					found[needle] = true
				}
			}
			if keep := longest - 1; len(window) > keep {
				window = append(window[:0], window[len(window)-keep:]...)
			}
		}
		if errors.Is(err, io.EOF) {
			return found, nil
		}
		if err != nil {
			return nil, fmt.Errorf("io: transcript: read %s: %w", path, err)
		}
	}
}
