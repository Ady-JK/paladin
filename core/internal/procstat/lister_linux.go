//go:build linux

package procstat

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// clockTicks is USER_HZ, the unit of the cumulative CPU and start times
// in /proc/<pid>/stat. Linux exposes USER_HZ as 100 on every
// architecture (it is not related to CONFIG_HZ).
const clockTicks = 100

// defaultLister reads /proc directly. Compared with shelling out to ps
// it needs no fork/exec or PATH, cannot fail because ps is missing, and
// reports the cumulative CPU time Snapshot needs for an instantaneous
// percentage instead of ps's lifetime average.
func defaultLister(all bool) ([]Proc, error) { return listProc(all) }

// listProc walks /proc. all=false reads only this process.
func listProc(all bool) ([]Proc, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	uptime := procUptime()
	self := os.Getpid()
	procs := make([]Proc, 0, len(entries))
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || (!all && pid != self) {
			continue
		}
		p, err := readProcStat(pid)
		if err != nil {
			continue // exited between ReadDir and the read
		}
		p.CPU = procLifetimeCPU(p, uptime)
		procs = append(procs, p)
	}
	return procs, nil
}

// readProcStat parses one /proc/<pid>/stat row. The comm field is
// wrapped in parentheses and may itself contain spaces or ')' — parse
// from the last ')' so the numbered fields stay aligned.
func readProcStat(pid int) (Proc, error) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return Proc{}, err
	}
	text := string(data)
	end := strings.LastIndexByte(text, ')')
	if end < 0 || end+2 > len(text) {
		return Proc{}, fmt.Errorf("procstat: malformed stat for pid %d", pid)
	}
	f := strings.Fields(text[end+2:])
	if len(f) < 22 {
		return Proc{}, fmt.Errorf("procstat: short stat for pid %d", pid)
	}
	// Field 3 (state) is f[0]; the rest are offset from there.
	ppid, e1 := strconv.Atoi(f[1])
	pgid, e2 := strconv.Atoi(f[2])
	utime, e3 := strconv.ParseUint(f[11], 10, 64)
	stime, e4 := strconv.ParseUint(f[12], 10, 64)
	start, e5 := strconv.ParseUint(f[19], 10, 64)
	rssPages, e6 := strconv.ParseInt(f[21], 10, 64)
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil || e6 != nil {
		return Proc{}, fmt.Errorf("procstat: bad stat fields for pid %d", pid)
	}
	if rssPages < 0 {
		rssPages = 0
	}
	return Proc{
		PID:     pid,
		PPID:    ppid,
		PGID:    pgid,
		CPUTime: ticks(utime + stime),
		Start:   start,
		RSS:     rssPages * int64(os.Getpagesize()),
	}, nil
}

// procUptime is seconds since boot, or 0 when /proc/uptime is unreadable.
func procUptime() float64 {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	field := strings.Fields(string(data))
	if len(field) == 0 {
		return 0
	}
	up, err := strconv.ParseFloat(field[0], 64)
	if err != nil {
		return 0
	}
	return up
}

// procLifetimeCPU is the same average ps %cpu reports, used as the
// first-sample value before two samples give a real rate.
func procLifetimeCPU(p Proc, uptime float64) float64 {
	if uptime <= 0 || p.Start == 0 {
		return 0
	}
	elapsed := uptime - float64(p.Start)/clockTicks
	if elapsed <= 0 {
		return 0
	}
	return float64(p.CPUTime) / float64(time.Second) / elapsed * 100
}

// ticks converts clock ticks to a duration.
func ticks(n uint64) time.Duration {
	return time.Duration(n) * time.Second / clockTicks
}
