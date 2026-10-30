// disk.go is the on-disk layout: folder names, transcript scanning
// and meta files.

package transcript

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/atomicfile"
	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/syserr"
	"github.com/google/uuid"
)

// EncodeCwd encodes a cwd to a namespace dir: escapes `-`, `:`, `/`,
// plus a hash suffix so similar inputs never collide. Reads also probe
// the legacy layout.
func EncodeCwd(cwd string) string {
	name := strings.ReplaceAll(cwd, "\\", "/")
	sum := sha256.Sum256([]byte(name))
	suffix := "-" + hex.EncodeToString(sum[:])[:8]
	name = strings.ReplaceAll(name, "-", "-0")
	name = strings.ReplaceAll(name, ":", "-1")
	name = strings.ReplaceAll(name, "/", "--")
	if name == "" || name == "." || name == ".." {
		return "_"
	}
	if isReservedWindowsName(name) {
		return "_" + name + suffix
	}
	return name + suffix
}

func legacyEncodeCwd(cwd string) string {
	name := strings.ReplaceAll(cwd, "\\", "/")
	name = strings.ReplaceAll(name, ":", "-")
	name = strings.ReplaceAll(name, "/", "--")
	if name == "" || name == "." || name == ".." {
		return "_"
	}
	if isReservedWindowsName(name) {
		return "_" + name
	}
	return name
}

func sessionDirs(root, cwd string) []string {
	fresh := filepath.Join(root, EncodeCwd(cwd))
	legacy := filepath.Join(root, legacyEncodeCwd(cwd))
	if fresh == legacy {
		return []string{fresh}
	}
	return []string{fresh, legacy}
}

func isReservedWindowsName(name string) bool {
	device := name
	if i := strings.IndexByte(device, '.'); i >= 0 {
		device = device[:i]
	}
	switch strings.ToUpper(device) {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(device) == 4 {
		upper := strings.ToUpper(device)
		prefix := upper[:3]
		last := device[3]
		if (prefix == "COM" || prefix == "LPT") && last >= '1' && last <= '9' {
			return true
		}
	}
	return false
}

func ValidID(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	if strings.ContainsAny(id, "/\\") {
		return false
	}
	_, err := uuid.Parse(id)
	return err == nil
}

func LoadTranscript(dir string) ([]Message, error) {
	var out []Message
	err := scanTranscript(dir, func(m Message) { out = append(out, m) })
	return out, err
}

// scanTranscript streams messages.jsonl, calling fn per message, so a
// long transcript never sits in memory as raw bytes. A complete line
// that does not decode is an error; an unterminated last line is a
// torn append (the process died mid-write) and is skipped.
func scanTranscript(dir string, fn func(Message)) error {
	f, err := os.Open(filepath.Join(dir, "messages.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("io: open transcript: %s", syserr.Str(err))
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, 64*1024)
	for {
		line, rerr := br.ReadBytes('\n')
		if rerr != nil && rerr != io.EOF {
			return fmt.Errorf("io: read transcript: %s", syserr.Str(rerr))
		}
		torn := rerr == io.EOF
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			var msg Message
			if err := json.Unmarshal(trimmed, &msg); err != nil {
				if torn {
					return nil
				}
				return fmt.Errorf("io: decode message: %s", syserr.Str(err))
			}
			fn(msg)
		}
		if torn {
			return nil
		}
	}
}

// dropTornTail truncates f after its last newline so an append never
// glues onto a half-written line.
func dropTornTail(f *os.File) error {
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return err
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, st.Size()-1); err != nil {
		return err
	}
	if last[0] == '\n' {
		return nil
	}
	const chunk = 64 * 1024
	end := st.Size()
	buf := make([]byte, chunk)
	for end > 0 {
		start := end - chunk
		if start < 0 {
			start = 0
		}
		n, err := f.ReadAt(buf[:end-start], start)
		if err != nil && err != io.EOF {
			return err
		}
		if k := bytes.LastIndexByte(buf[:n], '\n'); k >= 0 {
			return f.Truncate(start + int64(k) + 1)
		}
		end = start
	}
	return f.Truncate(0)
}

func writeMeta(dir string, meta Meta) error {
	data, err := jsonx.MarshalIndent(meta)
	if err != nil {
		return fmt.Errorf("io: encode meta: %s", syserr.Str(err))
	}
	return atomicWrite(dir, "meta.json", data)
}

// atomicWrite replaces dir/name so a crash never leaves a half-written
// file behind; session files are owner-only (they may hold code).
func atomicWrite(dir, name string, data []byte) error {
	if err := atomicfile.Write(filepath.Join(dir, name), data); err != nil {
		return fmt.Errorf("io: write %s: %s", name, syserr.Str(err))
	}
	return nil
}

func readMeta(dir string) (Meta, error) {
	data, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return Meta{}, fmt.Errorf("io: read meta: %s", syserr.Str(err))
	}
	var meta Meta
	if err := json.Unmarshal(data, &meta); err != nil {
		return Meta{}, fmt.Errorf("io: decode meta: %s", syserr.Str(err))
	}
	if meta.SchemaVersion == 0 {
		meta.SchemaVersion = SchemaVersion
	}
	if meta.SchemaVersion > SchemaVersion {
		return Meta{}, fmt.Errorf("version too new: schema %d > supported %d", meta.SchemaVersion, SchemaVersion)
	}
	return meta, nil
}
