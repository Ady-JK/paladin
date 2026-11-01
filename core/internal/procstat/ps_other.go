//go:build !unix

package procstat

import "errors"

// listPS is unsupported off unix; snapshots report Supported=false.
func listPS(bool) ([]Proc, error) {
	return nil, errors.New("procstat: unsupported on this platform")
}

// defaultLister matches listPS off unix.
func defaultLister(all bool) ([]Proc, error) { return listPS(all) }
