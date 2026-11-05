//go:build !linux

package extension

import "os"

// extraStamp has nothing to add where the file info carries no change
// time; the modification time and size are the stamp.
func extraStamp(os.FileInfo) string { return "" }
