package zmq

import "time"

// heartbeatState is protected by its owning conn's mutex.
type heartbeatState struct {
	lastRecv    time.Time
	pendingPing uint64
	nextSeq     uint64
}

func newHeartbeatState(now time.Time) heartbeatState {
	return heartbeatState{
		lastRecv: now,
		nextSeq:  1,
	}
}

func (h *heartbeatState) observe(now time.Time) {
	h.lastRecv = now
	h.pendingPing = 0
}

func (h *heartbeatState) shouldPing(now time.Time, interval time.Duration) bool {
	if h.pendingPing != 0 || now.Sub(h.lastRecv) < interval {
		return false
	}
	if h.nextSeq == 0 {
		h.nextSeq = 1
	}
	h.pendingPing = h.nextSeq
	h.nextSeq++
	return true
}

func (h heartbeatState) timedOut(now time.Time, peerTimeout time.Duration) bool {
	return now.Sub(h.lastRecv) >= peerTimeout
}
