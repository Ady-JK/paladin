//go:build !linux

package extension

import "errors"

// runnerResidentBytes is Linux-only; other platforms skip the soft
// memory warning rather than guessing from ps output.
func runnerResidentBytes(int) (int64, error) {
	return 0, errors.New("resident memory is not readable here")
}
