package plugin

import "sync"

// cappedBuffer is a thread-safe byte buffer that drops further writes after
// it reaches its cap. Used for capturing stderr from plugins so a runaway
// plugin can't blow up the daemon's memory.
type cappedBuffer struct {
	mu  sync.Mutex
	buf []byte
	cap int
}

func newCappedBuffer(cap int) *cappedBuffer { return &cappedBuffer{cap: cap} }

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.cap - len(b.buf)
	if remaining <= 0 {
		return len(p), nil // pretend we wrote it
	}
	if len(p) > remaining {
		b.buf = append(b.buf, p[:remaining]...)
	} else {
		b.buf = append(b.buf, p...)
	}
	return len(p), nil
}

func (b *cappedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

func (b *cappedBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.buf)
}
