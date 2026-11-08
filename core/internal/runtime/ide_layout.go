package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Ady-JK/paladin/core/internal/atomicfile"
)

const maxIDELayoutBytes = 8 << 20

func (r *AgentRuntime) ideLayoutPath(cwd string) (string, error) {
	if !filepath.IsAbs(cwd) {
		return "", fmt.Errorf("IDE workspace must be an absolute path")
	}
	info, err := os.Stat(cwd)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("IDE workspace is not a directory")
	}
	sum := sha256.Sum256([]byte(filepath.Clean(cwd)))
	return filepath.Join(r.dataDir, "ide-layout", hex.EncodeToString(sum[:])+".json"), nil
}

// IDELayoutGet returns this workspace's private layout and recoverable drafts.
func (r *AgentRuntime) IDELayoutGet(cwd string) (json.RawMessage, error) {
	path, err := r.ideLayoutPath(cwd)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return json.RawMessage("null"), nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("IDE layout is not a regular file")
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return json.RawMessage("null"), nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxIDELayoutBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxIDELayoutBytes || !json.Valid(data) {
		return nil, fmt.Errorf("invalid or oversized IDE layout")
	}
	return json.RawMessage(data), nil
}

// IDELayoutSet atomically replaces a bounded owner-only layout file.
func (r *AgentRuntime) IDELayoutSet(cwd string, data json.RawMessage) error {
	path, err := r.ideLayoutPath(cwd)
	if err != nil {
		return err
	}
	if len(data) > maxIDELayoutBytes || !json.Valid(data) || len(data) == 0 || data[0] != '{' {
		return fmt.Errorf("invalid or oversized IDE layout")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return atomicfile.Write(path, data)
}
