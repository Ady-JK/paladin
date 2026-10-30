package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Ady-JK/paladin/core/internal/redact"
)

var logger *logWriter

// logWriter timestamps each line and tees it to stderr and
// <dataDir>/logs/paladin.log.
type logWriter struct {
	mu   sync.Mutex
	file *os.File
}

func (l *logWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	line := fmt.Sprintf("%s %s", time.Now().UTC().Format(time.RFC3339), redact.String(string(p)))
	if _, err := os.Stderr.WriteString(line); err != nil {
		return 0, err
	}
	if l.file != nil {
		if _, err := l.file.WriteString(line); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

func logf(format string, args ...any) {
	if logger == nil {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
		return
	}
	fmt.Fprintf(logger, format+"\n", args...)
}

func initLogging(dataDir string) {
	logsDir := filepath.Join(dataDir, "logs")
	_ = os.MkdirAll(logsDir, 0o755)
	f, err := os.OpenFile(filepath.Join(logsDir, "paladin.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		f = nil
	}
	logger = &logWriter{file: f}
}
