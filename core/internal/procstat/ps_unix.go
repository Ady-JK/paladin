//go:build unix

package procstat

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"time"
)

// listPS runs ps once. all=false asks only for this process's row. The
// time column is cumulative CPU time, which Snapshot turns into a rate;
// %cpu is the lifetime average and only used for a first sighting.
func listPS(all bool) ([]Proc, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	args := []string{"-o", "pid=,ppid=,pgid=,%cpu=,rss=,time="}
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
