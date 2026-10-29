// Package syserr formats OS errors as "No such file or directory
// (os error 2)": readable, with the errno for bug reports.
package syserr

import (
	"errors"
	"io/fs"
	"syscall"
)

// Str renders err as "<description> (os error N)" for common errnos.
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
	// errors.Is unwraps generic wrappers that os.IsNotExist misses.
	if errors.Is(err, fs.ErrNotExist) {
		return "No such file or directory (os error 2)"
	}
	if errors.Is(err, fs.ErrPermission) {
		return "Permission denied (os error 13)"
	}
	return err.Error()
}
