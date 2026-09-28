package outbox

import (
	"context"
	"sync"
)

// IdempotentSink 是幂等下游：按消息标识去重，
// 重复投递只记录尝试次数，不会重复应用。
type IdempotentSink struct {
	mu       sync.Mutex
	seen     map[uint64]bool
	applied  []Message // 实际应用的消息序列（去重后）
	attempts []uint64  // 全部投递尝试（含重复）
}

// NewIdempotentSink 创建幂等下游。
func NewIdempotentSink() *IdempotentSink {
	return &IdempotentSink{seen: make(map[uint64]bool)}
}

// Deliver 实现 Sink：首次见到的消息被应用，重复消息被去重。
func (s *IdempotentSink) Deliver(_ context.Context, msg Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts = append(s.attempts, msg.ID)
	if s.seen[msg.ID] {
		return nil
	}
	s.seen[msg.ID] = true
	s.applied = append(s.applied, msg)
	return nil
}

// Applied 返回下游实际应用的消息序列。
func (s *IdempotentSink) Applied() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Message, len(s.applied))
	copy(out, s.applied)
	return out
}

// Attempts 返回全部投递尝试的消息标识序列（含重复）。
func (s *IdempotentSink) Attempts() []uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]uint64, len(s.attempts))
	copy(out, s.attempts)
	return out
}

// Duplicates 返回被幂等去重的重复投递次数。
func (s *IdempotentSink) Duplicates() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.attempts) - len(s.applied)
}
