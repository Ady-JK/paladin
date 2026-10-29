package main

import (
	"fmt"
	"runtime/debug"
)

// guard runs fn and converts a panic into an error so one bad request
// fails on its own instead of killing the sidecar (and every live chat).
func guard(what string, fn func()) (err error) {
	defer func() {
		if p := recover(); p != nil {
			logf("ERROR %s panicked: %v\n%s", what, p, debug.Stack())
			err = fmt.Errorf("internal error in %s: %v", what, p)
		}
	}()
	fn()
	return nil
}
