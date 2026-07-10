package protocol

import (
	"context"
	"errors"
	"sync"
)

type Window struct {
	mu        sync.Mutex
	changed   chan struct{}
	available int
}

func NewWindow(size int) *Window {
	if size < 0 {
		size = 0
	}
	return &Window{available: size, changed: make(chan struct{})}
}

func (w *Window) Available() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.available
}

func (w *Window) Acquire(ctx context.Context, n int) error {
	if n < 0 {
		return errors.New("protocol: window acquire size must be non-negative")
	}
	if n == 0 {
		return nil
	}
	for {
		w.mu.Lock()
		w.initLocked()
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
	if n <= 0 {
		return
	}
	w.mu.Lock()
	w.initLocked()
	w.available += n
	close(w.changed)
	w.changed = make(chan struct{})
	w.mu.Unlock()
}

func (w *Window) initLocked() {
	if w.changed == nil {
		w.changed = make(chan struct{})
	}
}
