package scope

import (
	"sync"
	"time"
)

// Scope 是作用域树中的一个节点，带截止时间与取消传播。
type Scope struct {
	mu       sync.Mutex
	now      func() time.Time
	deadline time.Time

	parent   *Scope
	children map[*Scope]struct{}

	done   chan struct{}
	closed bool
	err    error
	reason Reason
	origin *Scope
	hooks  []func()
}

// NewRoot 创建根作用域。now 为注入时钟；deadline 为零值表示无截止时间。
func NewRoot(now func() time.Time, deadline time.Time) *Scope {
	return &Scope{
		now:      now,
		deadline: deadline,
		children: make(map[*Scope]struct{}),
		done:     make(chan struct{}),
	}
}

// Child 派生子作用域。deadline 为零值表示继承父的；
// 声明得比父更晚时会被收紧成父的。在已结束的父上派生时，
// 子作用域立即以 ErrAncestorEnded 结束。
func (s *Scope) Child(deadline time.Time) *Scope {
	s.mu.Lock()
	effective := deadline
	if effective.IsZero() {
		effective = s.deadline
	} else if !s.deadline.IsZero() && effective.After(s.deadline) {
		effective = s.deadline
	}
	c := &Scope{
		now:      s.now,
		deadline: effective,
		parent:   s,
		children: make(map[*Scope]struct{}),
		done:     make(chan struct{}),
	}
	if s.closed {
		reason, origin := s.reason, s.origin
		s.mu.Unlock()
		c.finish(ErrAncestorEnded, reason, origin)
		return c
	}
	s.children[c] = struct{}{}
	s.mu.Unlock()
	return c
}

// Deadline 返回本作用域的实际（已收紧）截止时间，零值表示无截止时间。
func (s *Scope) Deadline() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deadline
}

// Done 返回在作用域结束时关闭的 channel，只关闭一次。
func (s *Scope) Done() <-chan struct{} {
	return s.done
}

// Err 返回结束错误，未结束时为 nil。
func (s *Scope) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Reason 返回结束原因的文字说明，未结束时为空。
func (s *Scope) Reason() Reason {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reason
}

// Origin 返回最早触发本次结束的作用域，未结束时为 nil。
func (s *Scope) Origin() *Scope {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.origin
}
