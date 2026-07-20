package zmq

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/hunyxv/zrpc/status"
)

type sendBudget struct {
	mu       sync.Mutex
	maxCount int
	count    int
	maxBytes int64
	bytes    int64
	changed  chan struct{}
}

type sendReservation struct {
	budget *sendBudget
	bytes  int64
	once   sync.Once
}

func newSendBudget(maxCount int, maxBytes int64) (*sendBudget, error) {
	if maxCount <= 0 {
		return nil, fmt.Errorf("zmq: send budget count must be positive: %d", maxCount)
	}
	if maxBytes <= 0 {
		return nil, fmt.Errorf("zmq: send budget bytes must be positive: %d", maxBytes)
	}
	return &sendBudget{
		maxCount: maxCount,
		maxBytes: maxBytes,
		changed:  make(chan struct{}),
	}, nil
}

func (b *sendBudget) acquire(ctx context.Context, n int64) (*sendReservation, error) {
	if n < 0 {
		return nil, errors.New("zmq: send budget size must not be negative")
	}
	if n > b.maxBytes {
		return nil, status.Error(status.ResourceExhausted, "zmq: frame exceeds send queue byte limit")
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		b.mu.Lock()
		if err := ctx.Err(); err != nil {
			b.mu.Unlock()
			return nil, err
		}
		if b.canAcquireLocked(n) {
			b.count++
			b.bytes += n
			b.mu.Unlock()
			return &sendReservation{budget: b, bytes: n}, nil
		}
		changed := b.changed
		b.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

func (b *sendBudget) tryAcquire(n int64) (*sendReservation, bool) {
	if n < 0 || n > b.maxBytes {
		return nil, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.canAcquireLocked(n) {
		return nil, false
	}
	b.count++
	b.bytes += n
	return &sendReservation{budget: b, bytes: n}, true
}

func (r *sendReservation) release() {
	if r == nil || r.budget == nil {
		return
	}
	r.once.Do(func() {
		r.budget.release(r.bytes)
	})
}

func (b *sendBudget) release(n int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.count == 0 || n < 0 || n > b.bytes {
		return
	}
	b.count--
	b.bytes -= n
	close(b.changed)
	b.changed = make(chan struct{})
}

func (b *sendBudget) canAcquireLocked(n int64) bool {
	return b.count < b.maxCount && n <= b.maxBytes-b.bytes
}
