package zmq

import (
	"testing"
	"time"
)

func TestHeartbeatActivitySuppressesPing(t *testing.T) {
	h := newHeartbeatState(time.Unix(0, 0))
	h.observe(time.Unix(9, 0))
	if h.shouldPing(time.Unix(10, 0), 10*time.Second) {
		t.Fatal("recent activity should suppress ping")
	}
}

func TestHeartbeatShouldPingOnceAfterIdleInterval(t *testing.T) {
	start := time.Unix(100, 0)
	h := newHeartbeatState(start)

	if h.shouldPing(start.Add(10*time.Second-time.Nanosecond), 10*time.Second) {
		t.Fatal("shouldPing() before interval = true, want false")
	}
	if !h.shouldPing(start.Add(10*time.Second), 10*time.Second) {
		t.Fatal("shouldPing() at interval = false, want true")
	}
	if h.pendingPing != 1 || h.nextSeq != 2 {
		t.Fatalf("pendingPing = %d, nextSeq = %d; want 1, 2", h.pendingPing, h.nextSeq)
	}
	if h.shouldPing(start.Add(20*time.Second), 10*time.Second) {
		t.Fatal("shouldPing() with pending ping = true, want false")
	}
	if h.pendingPing != 1 || h.nextSeq != 2 {
		t.Fatalf("repeated shouldPing changed sequence: pendingPing = %d, nextSeq = %d", h.pendingPing, h.nextSeq)
	}
}

func TestHeartbeatObserveClearsPendingPing(t *testing.T) {
	start := time.Unix(100, 0)
	h := newHeartbeatState(start)
	if !h.shouldPing(start.Add(10*time.Second), 10*time.Second) {
		t.Fatal("shouldPing() = false, want true")
	}

	observed := start.Add(11 * time.Second)
	h.observe(observed)
	if h.lastRecv != observed {
		t.Fatalf("lastRecv = %v, want %v", h.lastRecv, observed)
	}
	if h.pendingPing != 0 {
		t.Fatalf("pendingPing = %d, want 0", h.pendingPing)
	}
	if h.shouldPing(observed.Add(10*time.Second-time.Nanosecond), 10*time.Second) {
		t.Fatal("recent observation did not reset idle interval")
	}
	if !h.shouldPing(observed.Add(10*time.Second), 10*time.Second) {
		t.Fatal("shouldPing() at next interval = false, want true")
	}
	if h.pendingPing != 2 || h.nextSeq != 3 {
		t.Fatalf("pendingPing = %d, nextSeq = %d; want 2, 3", h.pendingPing, h.nextSeq)
	}
}

func TestHeartbeatTimedOut(t *testing.T) {
	start := time.Unix(100, 0)
	const peerTimeout = 30 * time.Second
	tests := []struct {
		name string
		now  time.Time
		want bool
	}{
		{name: "before timeout", now: start.Add(peerTimeout - time.Nanosecond)},
		{name: "at timeout", now: start.Add(peerTimeout), want: true},
		{name: "after timeout", now: start.Add(peerTimeout + time.Second), want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHeartbeatState(start)
			if got := h.timedOut(tt.now, peerTimeout); got != tt.want {
				t.Fatalf("timedOut() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHeartbeatActivityResetsPeerTimeout(t *testing.T) {
	start := time.Unix(100, 0)
	h := newHeartbeatState(start)
	h.observe(start.Add(25 * time.Second))
	if h.timedOut(start.Add(30*time.Second), 30*time.Second) {
		t.Fatal("recent activity should reset peer timeout")
	}
}
