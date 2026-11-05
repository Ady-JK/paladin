//go:build linux

package extension

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// runnerResidentBytes reads a runner's resident set size. Linux exposes
// it in /proc; elsewhere the soft budget is skipped (see the non-Linux
// file).
func runnerResidentBytes(pid int) (int64, error) {
	if pid <= 0 {
		return 0, os.ErrNotExist
	}
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "statm"))
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return 0, os.ErrInvalid
	}
	pages, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return 0, err
	}
	return pages * int64(os.Getpagesize()), nil
}
