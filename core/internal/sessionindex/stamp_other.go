//go:build !darwin && !linux

package sessionindex

import "os"

// statStamp is unavailable here; fingerprints hash the file contents.
func statStamp(os.FileInfo) (string, bool) { return "", false }
