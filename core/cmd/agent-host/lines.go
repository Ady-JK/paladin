package main

import (
	"bufio"
	"errors"
	"io"
	"regexp"
)

// maxRequestLine is the longest request the host reads. A longer line is
// refused (and its id answered when it can be read) instead of ending the
// input loop, which would take every running turn down with it.
const maxRequestLine = 16 * 1024 * 1024

// overPrefixBytes is how much of a refused line is kept to find its id.
const overPrefixBytes = 4096

// readLines calls handle with each newline-terminated line of r, and the
// final unterminated one. A line longer than max is read through to its
// newline and discarded; tooLong gets its first bytes. The slice passed
// to a callback is only valid during the call. A read error other than EOF
// is returned.
func readLines(r io.Reader, max int, handle func(line []byte), tooLong func(prefix []byte)) error {
	reader := bufio.NewReaderSize(r, 64*1024)
	var line []byte
	var prefix []byte
	over := false
	flush := func() {
		if over {
			tooLong(prefix)
		} else {
			handle(line)
		}
		line, prefix, over = line[:0], prefix[:0], false
	}
	for {
		chunk, err := reader.ReadSlice('\n')
		terminated := err == nil
		data := chunk
		if terminated {
			data = chunk[:len(chunk)-1]
		}
		if !over {
			if len(line)+len(data) > max {
				over = true
				prefix = append(prefix[:0], line...)
				if room := overPrefixBytes - len(prefix); room > 0 {
					prefix = append(prefix, data[:min(room, len(data))]...)
				}
				line = line[:0]
			} else {
				line = append(line, data...)
			}
		}
		switch {
		case terminated:
			flush()
		case errors.Is(err, bufio.ErrBufferFull):
			// The line continues in the next chunk.
		case errors.Is(err, io.EOF):
			if over || len(line) > 0 {
				flush()
			}
			return nil
		default:
			return err
		}
	}
}

var requestIDPattern = regexp.MustCompile(`"id"\s*:\s*("[^"\\]*"|\d+)`)

// requestIDFromPrefix finds the id of a request whose line was refused,
// so its caller gets an error instead of waiting out a deadline. It
// returns nil when the id is not a plain string or number.
func requestIDFromPrefix(prefix []byte) []byte {
	if m := requestIDPattern.FindSubmatch(prefix); m != nil {
		return append([]byte(nil), m[1]...)
	}
	return nil
}
