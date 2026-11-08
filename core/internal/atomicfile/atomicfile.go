// Package atomicfile replaces files so a crash leaves either the old
// or the new content on disk, never a torn mix.
package atomicfile

import (
	"os"
	"path/filepath"
)

// Write replaces path with data. It writes an owner-only (0600) temp
// file beside path, syncs it, renames it over path, and syncs the
// folder so the rename survives a crash. The folder must exist. The
// temp file is removed on every failure path.
func Write(path string, data []byte) error {
	return WriteMode(path, data, 0o600)
}

// Create atomically installs a new owner-only file and never replaces an
// existing path, even if another process creates it concurrently.
func Create(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err = writeAndClose(tmp, data, 0o600); err != nil {
		return err
	}
	if err = os.Link(tmpName, path); err != nil {
		return err
	}
	syncDir(dir)
	return nil
}

// WriteMode atomically replaces path with the supplied permission bits.
func WriteMode(path string, data []byte, mode os.FileMode) (err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()
	if err = writeAndClose(tmp, data, mode); err != nil {
		return err
	}
	if err = os.Rename(tmpName, path); err != nil {
		return err
	}
	syncDir(dir)
	return nil
}

func writeAndClose(tmp *os.File, data []byte, mode os.FileMode) error {
	_, err := tmp.Write(data)
	if err == nil {
		err = tmp.Chmod(mode.Perm())
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	return err
}

// syncDir makes a rename durable where the platform supports syncing
// a folder; elsewhere it is a no-op.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}
