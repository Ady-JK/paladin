package sessionindex

import (
	"fmt"
	"os"
	"syscall"
)

// statStamp identifies a file's state by size, modification time and status
// change time. The change time moves on every write even when the
// modification time is put back, so it stands in for hashing the bytes.
func statStamp(fi os.FileInfo) (string, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%d:%d:%d:%d", st.Size, st.Mtim.Nano(), st.Ctim.Nano(), st.Ino), true
}
