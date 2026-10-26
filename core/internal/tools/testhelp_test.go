package tools

import (
	"context"
	"time"
)

func sleepMs(ms int) {
	deadline := time.Now().Add(time.Duration(ms) * time.Millisecond)
	for time.Now().Before(deadline) {
		select {
		case <-context.Background().Done():
		case <-time.After(time.Millisecond):
		}
	}
}

func afterMs(ms int) <-chan time.Time {
	return time.After(time.Duration(ms) * time.Millisecond)
}
