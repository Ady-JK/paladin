package extension

import "sync"

// ringBuffer keeps the last n bytes written to it. A plugin's stderr is
// unbounded chatter; a fixed tail is what a user needs to see why it
// failed.
type ringBuffer struct {
	mu   sync.Mutex
	data []byte
	max  int
}

func newRingBuffer(max int) *ringBuffer { return &ringBuffer{max: max} }

// Write keeps the newest max bytes.
func (b *ringBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	if len(b.data) > b.max {
		b.data = append(b.data[:0], b.data[len(b.data)-b.max:]...)
	}
	return len(p), nil
}

// tail returns at most n bytes of what was written.
func (b *ringBuffer) tail(n int) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	data := b.data
	if n > 0 && len(data) > n {
		data = data[len(data)-n:]
	}
	return string(data)
}
