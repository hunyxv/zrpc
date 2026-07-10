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
	s.mu.Lock()
	s.sendClosed = true
	s.mu.Unlock()
}

func (s *Session) CloseRecv() {
	s.mu.Lock()
	s.recvClosed = true
	s.mu.Unlock()
}

func (s *Session) Reset(st ...*status.Status) {
	s.mu.Lock()
	s.reset = true
	s.sendClosed = true
	s.recvClosed = true
	if len(st) > 0 {
		s.resetStatus = cloneStatus(st[0])
	}
	s.mu.Unlock()
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
