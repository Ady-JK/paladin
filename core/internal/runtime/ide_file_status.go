package runtime

import (
	"fmt"
	"os"
	"time"
)

type IDEFileCheck struct {
	Path    string `json:"path"`
	Version string `json:"version"`
}
type IDEFileStatus struct {
	Path    string `json:"path"`
	State   string `json:"state"`
	Version string `json:"version,omitempty"`
}
type ideFileFingerprint struct {
	info    os.FileInfo
	version string
	checked time.Time
}

// IDEFileStatuses checks open files using metadata, hashing only changed or
// periodically revalidated files. The save path still checks the full hash.
func (r *AgentRuntime) IDEFileStatuses(cwd string, checks []IDEFileCheck) ([]IDEFileStatus, error) {
	if len(checks) > 128 {
		return nil, fmt.Errorf("at most 128 files may be checked at once")
	}
	out := make([]IDEFileStatus, 0, len(checks))
	for _, check := range checks {
		status := IDEFileStatus{Path: check.Path, State: "unavailable"}
		path, err := ideFilePath(cwd, check.Path)
		if err != nil {
			out = append(out, status)
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			out = append(out, status)
			continue
		}
		r.ideCheckMu.Lock()
		cached, ok := r.ideCheckCache[path]
		r.ideCheckMu.Unlock()
		var version string
		if ok && time.Since(cached.checked) < 30*time.Second && sameIDEFile(info, cached.info) {
			version = cached.version
		} else {
			file, err := IDEReadFile(cwd, path)
			if err != nil {
				out = append(out, status)
				continue
			}
			version = file.Version
			after, err := os.Stat(path)
			if err == nil && sameIDEFile(info, after) {
				r.ideCheckMu.Lock()
				if r.ideCheckCache == nil || len(r.ideCheckCache) >= 512 {
					r.ideCheckCache = make(map[string]ideFileFingerprint)
				}
				r.ideCheckCache[path] = ideFileFingerprint{info: after, version: version, checked: time.Now()}
				r.ideCheckMu.Unlock()
			}
		}
		if version == check.Version {
			status.State = "same"
		} else {
			status.State = "changed"
		}
		status.Version = version
		out = append(out, status)
	}
	return out, nil
}

func sameIDEFile(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}
