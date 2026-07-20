package zmq

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hunyxv/zrpc/status"
)

type budgetAcquireResult struct {
	reservation *sendReservation
	err         error
}

func TestNewSendBudgetRejectsInvalidLimits(t *testing.T) {
	for _, tt := range []struct {
		name     string
		maxCount int
		maxBytes int64
	}{
		{name: "zero count", maxCount: 0, maxBytes: 1},
		{name: "negative count", maxCount: -1, maxBytes: 1},
		{name: "zero bytes", maxCount: 1, maxBytes: 0},
		{name: "negative bytes", maxCount: 1, maxBytes: -1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := newSendBudget(tt.maxCount, tt.maxBytes); err == nil {
				t.Fatal("newSendBudget() error = nil, want non-nil")
			}
		})
	}
}

func TestSendBudgetCountBlocksUntilRelease(t *testing.T) {
	budget := mustNewSendBudget(t, 1, 100)
	first := mustAcquireBudget(t, budget, 10)

	result := acquireBudgetAsync(budget, context.Background(), 10)
	assertBudgetBlocked(t, result)

	first.release()
	second := assertBudgetAcquired(t, result)
	second.release()
}

func TestSendBudgetBytesBlockUntilRelease(t *testing.T) {
	budget := mustNewSendBudget(t, 2, 4)
	first := mustAcquireBudget(t, budget, 3)

	result := acquireBudgetAsync(budget, context.Background(), 2)
	assertBudgetBlocked(t, result)

	first.release()
	second := assertBudgetAcquired(t, result)
	second.release()
}

func TestSendBudgetAcquireHonorsContextCancellation(t *testing.T) {
	budget := mustNewSendBudget(t, 1, 100)
	first := mustAcquireBudget(t, budget, 10)

	ctx, cancel := context.WithCancel(context.Background())
	result := acquireBudgetAsync(budget, ctx, 10)
	assertBudgetBlocked(t, result)
	cancel()

	assertBudgetCanceled(t, result)
	first.release()
}

func TestSendBudgetCancellationWinsAfterReleaseWake(t *testing.T) {
	for i := 0; i < 100; i++ {
		budget := mustNewSendBudget(t, 1, 100)
		first := mustAcquireBudget(t, budget, 10)
		ctx, cancel := context.WithCancel(context.Background())
		result := acquireBudgetAsync(budget, ctx, 10)
		assertBudgetBlocked(t, result)

		cancel()
		first.release()
		assertBudgetCanceled(t, result)
	}
}

func TestSendBudgetReleaseWakesAllEligibleWaiters(t *testing.T) {
	budget := mustNewSendBudget(t, 2, 100)
	first := mustAcquireBudget(t, budget, 10)
	second := mustAcquireBudget(t, budget, 10)

	results := make(chan budgetAcquireResult, 2)
	go acquireBudgetInto(budget, context.Background(), 10, results)
	go acquireBudgetInto(budget, context.Background(), 10, results)
	assertBudgetBlocked(t, results)

	first.release()
	second.release()
	third := assertBudgetAcquired(t, results)
	fourth := assertBudgetAcquired(t, results)
	third.release()
	fourth.release()
}

func TestSendBudgetTryAcquire(t *testing.T) {
	budget := mustNewSendBudget(t, 2, 5)
	first, ok := budget.tryAcquire(3)
	if !ok {
		t.Fatal("tryAcquire(3) = false, want true")
	}
	if _, ok := budget.tryAcquire(3); ok {
		t.Fatal("tryAcquire(3) with insufficient bytes = true, want false")
	}
	second, ok := budget.tryAcquire(2)
	if !ok {
		t.Fatal("tryAcquire(2) = false, want true")
	}
	if _, ok := budget.tryAcquire(1); ok {
		t.Fatal("tryAcquire(1) at count limit = true, want false")
	}

	first.release()
	third, ok := budget.tryAcquire(3)
	if !ok {
		t.Fatal("tryAcquire(3) after release = false, want true")
	}
	second.release()
	third.release()
}

func TestSendBudgetAcquireRejectsSingleFrameOverLimit(t *testing.T) {
	budget := mustNewSendBudget(t, 1, 4)
	_, err := budget.acquire(context.Background(), 5)
	if got := status.FromError(err).Code; got != status.ResourceExhausted {
		t.Fatalf("acquire() status code = %v, want %v", got, status.ResourceExhausted)
	}
	reservation, ok := budget.tryAcquire(4)
	if !ok {
		t.Fatal("oversized acquire consumed budget")
	}
	reservation.release()
}

func TestSendReservationReleaseIsIdempotent(t *testing.T) {
	budget := mustNewSendBudget(t, 2, 30)
	first := mustAcquireBudget(t, budget, 10)
	second := mustAcquireBudget(t, budget, 20)

	first.release()
	first.release()
	third, ok := budget.tryAcquire(10)
	if !ok {
		t.Fatal("tryAcquire(10) after release = false, want true")
	}
	if _, ok := budget.tryAcquire(1); ok {
		t.Fatal("duplicate release freed another reservation")
	}

	second.release()
	third.release()
}

func mustNewSendBudget(t *testing.T, maxCount int, maxBytes int64) *sendBudget {
	t.Helper()
	budget, err := newSendBudget(maxCount, maxBytes)
	if err != nil {
		t.Fatalf("newSendBudget() error = %v", err)
	}
	return budget
}

func mustAcquireBudget(t *testing.T, budget *sendBudget, n int64) *sendReservation {
	t.Helper()
	reservation, err := budget.acquire(context.Background(), n)
	if err != nil {
		t.Fatalf("acquire() error = %v", err)
	}
	return reservation
}

func acquireBudgetAsync(budget *sendBudget, ctx context.Context, n int64) <-chan budgetAcquireResult {
	result := make(chan budgetAcquireResult, 1)
	go acquireBudgetInto(budget, ctx, n, result)
	return result
}

func acquireBudgetInto(budget *sendBudget, ctx context.Context, n int64, result chan<- budgetAcquireResult) {
	reservation, err := budget.acquire(ctx, n)
	result <- budgetAcquireResult{reservation: reservation, err: err}
}

func assertBudgetBlocked(t *testing.T, result <-chan budgetAcquireResult) {
	t.Helper()
	select {
	case got := <-result:
		t.Fatalf("acquire() returned while budget exhausted: %v", got.err)
	case <-time.After(10 * time.Millisecond):
	}
}

func assertBudgetAcquired(t *testing.T, result <-chan budgetAcquireResult) *sendReservation {
	t.Helper()
	select {
	case got := <-result:
		if got.err != nil {
			t.Fatalf("acquire() error = %v", got.err)
		}
		if got.reservation == nil {
			t.Fatal("acquire() reservation = nil")
		}
		return got.reservation
	case <-time.After(time.Second):
		t.Fatal("acquire() did not complete after budget release")
		return nil
	}
}

func assertBudgetCanceled(t *testing.T, result <-chan budgetAcquireResult) {
	t.Helper()
	select {
	case got := <-result:
		if !errors.Is(got.err, context.Canceled) {
			t.Fatalf("acquire() error = %v, want context.Canceled", got.err)
		}
		if got.reservation != nil {
			t.Fatal("canceled acquire returned a reservation")
		}
	case <-time.After(time.Second):
		t.Fatal("acquire() did not return after context cancellation")
	}
}
