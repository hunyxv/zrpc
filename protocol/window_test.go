package protocol

import (
	"context"
	"testing"
	"time"
)

func TestWindowAcquireRelease(t *testing.T) {
	window := NewWindow(10)

	if err := window.Acquire(context.Background(), 6); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	window.Release(4)

	if got, want := window.Available(), 8; got != want {
		t.Fatalf("Available() = %d, want %d", got, want)
	}
}

func TestWindowAcquireBlocksUntilRelease(t *testing.T) {
	window := NewWindow(5)
	if err := window.Acquire(context.Background(), 5); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- window.Acquire(context.Background(), 3)
	}()

	select {
	case err := <-done:
		t.Fatalf("Acquire() returned before Release(): %v", err)
	case <-time.After(10 * time.Millisecond):
	}

	window.Release(3)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Acquire() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Acquire() did not return after Release()")
	}
}

func TestWindowAcquireContextCanceled(t *testing.T) {
	window := NewWindow(1)
	if err := window.Acquire(context.Background(), 1); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := window.Acquire(ctx, 1); err == nil {
		t.Fatal("Acquire() error = nil, want non-nil")
	}
}

func TestWindowRejectsNegativeAcquire(t *testing.T) {
	window := NewWindow(1)

	if err := window.Acquire(context.Background(), -1); err == nil {
		t.Fatal("Acquire(-1) error = nil, want non-nil")
	}
	if got, want := window.Available(), 1; got != want {
		t.Fatalf("Available() after Acquire(-1) = %d, want %d", got, want)
	}
}

func TestWindowIgnoresNonPositiveRelease(t *testing.T) {
	window := NewWindow(3)

	window.Release(-1)
	window.Release(0)

	if got, want := window.Available(), 3; got != want {
		t.Fatalf("Available() after non-positive release = %d, want %d", got, want)
	}
}

func TestZeroValueWindowReleaseIsSafe(t *testing.T) {
	var window Window

	window.Release(1)

	if got, want := window.Available(), 1; got != want {
		t.Fatalf("Available() after zero-value Release(1) = %d, want %d", got, want)
	}
}
