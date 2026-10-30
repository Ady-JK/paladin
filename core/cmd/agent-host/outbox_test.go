package main

import (
	"testing"
	"time"
)

// A stalled writer must not block whoever reports a change, and
// values still arrive in push order once it resumes.
func TestOutboxNeverBlocksThePusher(t *testing.T) {
	release := make(chan struct{})
	var got []int
	o := newOutbox(func(v any) { <-release; got = append(got, v.(int)) })
	done := make(chan struct{})
	go func() {
		for i := range 100 {
			o.push(i)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("push blocked on a stalled writer")
	}
	close(release)
	o.flush()
	for i, v := range got {
		if v != i {
			t.Fatalf("out of order at %d: %v", i, got)
		}
	}
	if len(got) != 100 {
		t.Fatalf("got %d", len(got))
	}
}
