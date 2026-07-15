package protocol

import (
	"sync"

	"github.com/hunyxv/zrpc/status"
)

// Session 记录一个 RPC stream 的 half-close 和 reset 状态。
type Session struct {
	id          string
	mu          sync.Mutex
	sendClosed  bool
	recvClosed  bool
	reset       bool
	resetStatus *status.Status
}

// NewSession 创建指定 id 的 session。
func NewSession(id string) *Session {
	return &Session{id: id}
}

// ID 返回 session id。
func (s *Session) ID() string {
	return s.id
}

// CloseSend 标记发送方向关闭。
func (s *Session) CloseSend() {
	_ = s.TryCloseSend()
}

// TryCloseSend 尝试关闭发送方向，并返回本次调用是否改变了状态。
func (s *Session) TryCloseSend() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reset || s.sendClosed {
		return false
	}
	s.sendClosed = true
	return true
}

// CloseRecv 标记接收方向关闭。
func (s *Session) CloseRecv() {
	_ = s.TryCloseRecv()
}

// TryCloseRecv 尝试关闭接收方向，并返回本次调用是否改变了状态。
func (s *Session) TryCloseRecv() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reset || s.recvClosed {
		return false
	}
	s.recvClosed = true
	return true
}

// Reset 将 session 标记为已重置。
func (s *Session) Reset(st ...*status.Status) {
	_ = s.TryReset(st...)
}

// TryReset 尝试重置 session，并返回本次调用是否改变了状态。
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

// CanSend 返回当前是否仍允许发送。
func (s *Session) CanSend() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.reset && !s.sendClosed
}

// CanRecv 返回当前是否仍允许接收。
func (s *Session) CanRecv() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.reset && !s.recvClosed
}

// SendClosed 返回发送方向是否已关闭。
func (s *Session) SendClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sendClosed
}

// RecvClosed 返回接收方向是否已关闭。
func (s *Session) RecvClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recvClosed
}

// IsReset 返回 session 是否已重置。
func (s *Session) IsReset() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reset
}

// IsClosed 返回双向是否都已关闭。
func (s *Session) IsClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sendClosed && s.recvClosed
}

// ResetStatus 返回 reset 状态副本。
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
