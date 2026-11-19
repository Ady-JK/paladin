package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitLoggingSendsStdlibLogToTheLogFileOwnerOnly(t *testing.T) {
	prevOut, prevFlags, prevLogger, prevRoot := log.Writer(), log.Flags(), logger, dataRoot
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
		logger, dataRoot = prevLogger, prevRoot
	})
	dir := t.TempDir()
	initLogging(dir)
	log.Printf("runtime says %s", "hello")
	logf("host says hi")

	path := filepath.Join(dir, "logs", "paladin.log")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "runtime says hello") || !strings.Contains(text, "host says hi") {
		t.Fatalf("log file = %q", text)
	}
	// One timestamp per line: the stdlib one is switched off.
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if strings.Count(line, "T") > 1 && strings.Count(line, "Z ") > 1 {
			t.Fatalf("line carries two timestamps: %q", line)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("paladin.log mode = %o", info.Mode().Perm())
	}
	dirInfo, _ := os.Stat(filepath.Dir(path))
	if dirInfo.Mode().Perm()&0o077 != 0 {
		t.Fatalf("logs folder mode = %o", dirInfo.Mode().Perm())
	}
}
