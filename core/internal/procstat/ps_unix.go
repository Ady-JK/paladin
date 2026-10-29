//go:build unix

package procstat

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"time"
)

// listPS runs ps once. all=false asks only for this process's row.
func listPS(all bool) ([]Proc, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	args := []string{"-o", "pid=,ppid=,pgid=,%cpu=,rss="}
	if all {
		args = append([]string{"-ax"}, args...)
	} else {
		args = append(args, "-p", strconv.Itoa(os.Getpid()))
	}
	out, err := exec.CommandContext(ctx, "ps", args...).Output()
	if err != nil {
		return nil, err
	}
	return parsePS(string(out)), nil
}
