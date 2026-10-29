//go:build !unix

package memory

import (
	"errors"
	"os"
	"time"
)

// lockFile falls back to an O_EXCL lock file with retry where flock is
// unavailable. A lock older than 30s is treated as abandoned.
func lockFile(path string) (func(), error) {
	deadline := time.Now().Add(10 * time.Second)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			f.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if st, serr := os.Stat(path); serr == nil && time.Since(st.ModTime()) > 30*time.Second {
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, errors.New("timed out waiting for the memory lock")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
