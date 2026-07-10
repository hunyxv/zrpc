package protocol

import (
	"context"
	"errors"
	"sync"

	"github.com/hunyxv/zrpc/status"
)

type Window struct {
	mu        sync.Mutex
	changed   chan struct{}
	available int
	limit     int
}

func NewWindow(size int) *Window {
	if size < 0 {
		size = 0
	}
	return &Window{available: size, limit: size, changed: make(chan struct{})}
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
		if n > w.limit {
			w.mu.Unlock()
			return status.Error(status.ResourceExhausted, "protocol: window acquire exceeds limit")
		}
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

func (w *Window) Release(n int) error {
	if n <= 0 {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.initLocked()
	if n > w.limit-w.available {
		return status.Error(status.ResourceExhausted, "protocol: window release exceeds limit")
	}
	w.available += n
	close(w.changed)
	w.changed = make(chan struct{})
	return nil
}

func (w *Window) initLocked() {
	if w.changed == nil {
		w.changed = make(chan struct{})
		if w.limit == 0 && w.available == 0 {
			w.limit = maxWindowLimit()
		}
	}
}

func maxWindowLimit() int {
	return int(^uint(0) >> 1)
}
