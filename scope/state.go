package scope

import "time"

// markEnded 尝试以首个判定结果落定终态。
// 返回 false 表示作用域此前已经结束，本次判定被丢弃。
func (s *Scope) markEnded(err error, reason Reason, origin *Scope) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return false
	}
	s.ended = true
	s.err = err
	s.reason = reason
	s.origin = origin
	return true
}

// Deadline 返回作用域的实际截止时间（零值表示无截止时间）。
func (s *Scope) Deadline() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deadline
}

// Done 返回作用域结束时关闭的 channel；每个作用域只关闭一次。
func (s *Scope) Done() <-chan struct{} {
	return s.done
}

// Err 返回作用域的结束错误；未结束时为 nil。
func (s *Scope) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Reason 返回结束原因的文字说明；连坐后代透出的是最早触发者的原文。
func (s *Scope) Reason() Reason {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reason
}

// Origin 返回最早触发本次结束的作用域（自身或某个祖先）。
func (s *Scope) Origin() *Scope {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.origin
}
