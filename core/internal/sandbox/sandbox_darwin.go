//go:build darwin

package sandbox

import (
	"fmt"
	"os"
	"strings"
)

const sandboxExec = "/usr/bin/sandbox-exec"

func kind() string {
	if _, err := os.Stat(sandboxExec); err != nil {
		return ""
	}
	return "seatbelt"
}

// seatbeltBase allows everything, then denies writes; the later allow
// rules re-open the device files and roots (SBPL: the last match wins).
const seatbeltBase = `(version 1)
(allow default)
(deny file-write*)
(allow file-write*
  (literal "/dev/null")
  (literal "/dev/zero")
  (literal "/dev/dtracehelper")
  (literal "/dev/stdout")
  (literal "/dev/stderr")
  (regex #"^/dev/tty")
  (regex #"^/dev/fd/"))
`

func wrap(roots []string, argv []string) ([]string, error) {
	var b strings.Builder
	b.WriteString(seatbeltBase)
	args := []string{sandboxExec}
	if len(roots) > 0 {
		b.WriteString("(allow file-write*")
		for i := range roots {
			fmt.Fprintf(&b, "\n  (subpath (param \"W%d\"))", i)
		}
		b.WriteString(")\n")
	}
	args = append(args, "-p", b.String())
	for i, r := range roots {
		args = append(args, "-D", fmt.Sprintf("W%d=%s", i, r))
	}
	return append(args, argv...), nil
}

// RunHelper is the Linux re-exec entry point; macOS never uses it.
func RunHelper([]string) int { return 2 }
