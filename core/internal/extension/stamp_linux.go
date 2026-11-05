//go:build linux

package extension

import (
	"fmt"
	"os"
	"syscall"
)

// extraStamp adds the inode change time: a file overwritten twice inside
// one filesystem timestamp tick keeps its mtime but not its ctime.
func extraStamp(stat os.FileInfo) string {
	if st, ok := stat.Sys().(*syscall.Stat_t); ok {
		return fmt.Sprintf(":%d:%d", st.Ctim.Sec, st.Ctim.Nsec)
	}
	return ""
}
