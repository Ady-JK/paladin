package syserr

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
	"testing"
)

func TestStrNilIsEmpty(t *testing.T) {
	if got := Str(nil); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestStrFormatsCommonErrnosWithCode(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{syscall.ENOENT, "No such file or directory (os error 2)"},
		{syscall.EACCES, "Permission denied (os error 13)"},
		{syscall.ENOTDIR, "Not a directory (os error 20)"},
		{syscall.EEXIST, "File exists (os error 17)"},
	}
	for _, tc := range cases {
		if got := Str(tc.err); got != tc.want {
			t.Errorf("Str(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

func TestStrUnwrapsWrappedErrno(t *testing.T) {
	err := fmt.Errorf("open /nope: %w", syscall.ENOENT)
	if got := Str(err); got != "No such file or directory (os error 2)" {
		t.Fatalf("got %q", got)
	}
}

func TestStrFallsBackToOsPredicates(t *testing.T) {
	// A path error is recognised through os.IsNotExist even when the
	// concrete errno is not one of the explicitly formatted ones.
	err := &os.PathError{Op: "open", Path: "/nope", Err: syscall.ENOENT}
	if got := Str(err); got != "No such file or directory (os error 2)" {
		t.Fatalf("got %q", got)
	}
	wrapped := &fs.PathError{Op: "stat", Path: "/root/secret", Err: syscall.EACCES}
	if got := Str(wrapped); got != "Permission denied (os error 13)" {
		t.Fatalf("got %q", got)
	}
	// Wrapped sentinel errors hit the os.IsNotExist / os.IsPermission
	// fallbacks without any errno involved.
	if got := Str(fmt.Errorf("open: %w", fs.ErrNotExist)); got != "No such file or directory (os error 2)" {
		t.Fatalf("got %q", got)
	}
	if got := Str(fmt.Errorf("open: %w", fs.ErrPermission)); got != "Permission denied (os error 13)" {
		t.Fatalf("got %q", got)
	}
}

func TestStrKeepsUnknownErrors(t *testing.T) {
	if got := Str(errors.New("boom")); got != "boom" {
		t.Fatalf("got %q", got)
	}
	// ENOSPC is formatted by neither branch, so Display text passes through.
	disk := syscall.ENOSPC
	if got := Str(disk); got != disk.Error() {
		t.Fatalf("got %q, want %q", got, disk.Error())
	}
}
