package protocol

import (
	"sync"

	"github.com/hunyxv/zrpc/status"
)

type Session struct {
	id          string
	mu          sync.Mutex
	sendClosed  bool
	recvClosed  bool
	reset       bool
	resetStatus *status.Status
}

func NewSession(id string) *Session {
	return &Session{id: id}
}

func (s *Session) ID() string {
	return s.id
}

func (s *Session) CloseSend() {
	_ = s.TryCloseSend()
}

func (s *Session) TryCloseSend() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reset || s.sendClosed {
		return false
	}
	s.sendClosed = true
	return true
}

func (s *Session) CloseRecv() {
	_ = s.TryCloseRecv()
}

func (s *Session) TryCloseRecv() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reset || s.recvClosed {
		return false
	}
	s.recvClosed = true
	return true
}

func (s *Session) Reset(st ...*status.Status) {
	_ = s.TryReset(st...)
}

func (s *Session) TryReset(st ...*status.Status) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reset {
		return false
	}
	s.reset = true
	s.sendClosed = true
	s.recvClosed = true
	if len(st) > 0 {
		s.resetStatus = cloneStatus(st[0])
	}
	return true
}

func (s *Session) CanSend() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.reset && !s.sendClosed
}

func (s *Session) CanRecv() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.reset && !s.recvClosed
}

func (s *Session) SendClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sendClosed
}

func (s *Session) RecvClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recvClosed
}

func (s *Session) IsReset() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reset
}

func (s *Session) IsClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sendClosed && s.recvClosed
}

func (s *Session) ResetStatus() *status.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneStatus(s.resetStatus)
}

func cloneStatus(st *status.Status) *status.Status {
	if st == nil {
		return nil
	}
	cp := *st
	cp.Details = append([]string(nil), st.Details...)
	return &cp
}
