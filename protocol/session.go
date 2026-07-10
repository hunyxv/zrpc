package protocol

import "sync"

type Session struct {
	id         string
	mu         sync.Mutex
	sendClosed bool
	recvClosed bool
	reset      bool
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

func (s *Session) Reset() {
	s.mu.Lock()
	s.reset = true
	s.sendClosed = true
	s.recvClosed = true
	s.mu.Unlock()
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
