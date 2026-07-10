package protocol

import (
	"context"
	"sync"
)

type Window struct {
	mu        sync.Mutex
	changed   chan struct{}
	available int
}

func NewWindow(size int) *Window {
	return &Window{available: size, changed: make(chan struct{})}
}

func (w *Window) Available() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.available
}

func (w *Window) Acquire(ctx context.Context, n int) error {
	for {
		w.mu.Lock()
		if w.available >= n {
			w.available -= n
			w.mu.Unlock()
			return nil
		}
		changed := w.changed
		w.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

func (w *Window) Release(n int) {
	w.mu.Lock()
	w.available += n
	close(w.changed)
	w.changed = make(chan struct{})
	w.mu.Unlock()
}
