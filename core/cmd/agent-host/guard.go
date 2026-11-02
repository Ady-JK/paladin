package main

import (
	"fmt"
	"runtime/debug"

	"github.com/Ady-JK/paladin/core/internal/errlog"
)

// guard runs fn and converts a panic into an error so one bad request
// fails on its own instead of killing the sidecar (and every live chat).
func guard(what string, fn func()) (err error) {
	defer func() {
		if p := recover(); p != nil {
			stack := debug.Stack()
			logf("ERROR %s panicked: %v\n%s", what, p, stack)
			errlog.Global(dataRoot, "panic:"+what, fmt.Sprintf("%v\n%s", p, stack))
			err = fmt.Errorf("internal error in %s: %v", what, p)
		}
	}()
	fn()
	return nil
}
