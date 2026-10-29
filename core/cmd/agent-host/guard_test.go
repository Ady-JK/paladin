package main

import (
	"strings"
	"testing"
)

func TestGuardTurnsPanicIntoError(t *testing.T) {
	err := guard("unary ping", func() { panic("kaboom") })
	if err == nil || !strings.Contains(err.Error(), "kaboom") || !strings.Contains(err.Error(), "unary ping") {
		t.Fatalf("got %v", err)
	}
	if err := guard("ok", func() {}); err != nil {
		t.Fatalf("got %v", err)
	}
}
