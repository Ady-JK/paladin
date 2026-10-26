// Package syserr formats OS errors like Rust's io::Error Display
// ("No such file or directory (os error 2)") so messages match the
// Rust core byte for byte.
package syserr

import (
	"errors"
	"os"
	"syscall"
)

// Str renders err in Rust io::Error Display form for common errnos.
func Str(err error) string {
	if err == nil {
		return ""
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case syscall.ENOENT:
			return "No such file or directory (os error 2)"
		case syscall.EACCES:
			return "Permission denied (os error 13)"
		case syscall.ENOTDIR:
			return "Not a directory (os error 20)"
		case syscall.EEXIST:
			return "File exists (os error 17)"
		}
	}
	if os.IsNotExist(err) {
		return "No such file or directory (os error 2)"
	}
	if os.IsPermission(err) {
		return "Permission denied (os error 13)"
	}
	return err.Error()
}
