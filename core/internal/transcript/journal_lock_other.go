//go:build !unix && !windows

package transcript

import "errors"

func tryJournalLock(string) (func(), bool, error) {
	return nil, false, errors.New("turn recovery locking is unavailable on this platform")
}
