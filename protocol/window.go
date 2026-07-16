package protocol

import (
	"context"
	"errors"
	"sync"

	"github.com/hunyxv/zrpc/status"
)

// Window 是用于 stream 流控的并发安全计数窗口。
type Window struct {
	mu        sync.Mutex
	changed   chan struct{}
	available int
	limit     int
}

// NewWindow 创建指定大小的窗口。
func NewWindow(size int) *Window {
	if size < 0 {
		size = 0
	}
	return &Window{available: size, limit: size, changed: make(chan struct{})}
}

// Available 返回当前可用窗口大小。
func (w *Window) Available() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.available
}

// Acquire 尝试占用 n 字节窗口，不足时阻塞直到窗口释放或 ctx 取消。
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

// Release 释放 n 字节窗口并唤醒等待者。
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

// ReleaseAll 将窗口恢复到上限并唤醒所有等待者。
func (w *Window) ReleaseAll() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.initLocked()
	if w.available == w.limit {
		return
	}
	w.available = w.limit
	close(w.changed)
	w.changed = make(chan struct{})
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
