// Package sched 提交器：校验时间戳，驱动漏桶排水与接纳。依赖 lb。
package sched

import (
	"errors"
	"sync"

	"ontology/lb"
)

// 哨兵错误，互不相同。
var (
	ErrNegativeT    = errors.New("sched: negative timestamp")
	ErrNonMonotonic = errors.New("sched: clock regression")
)

// Submitter 持有 lastT，保证时钟单调。并发安全。
type Submitter struct {
	mu    sync.Mutex
	b     *lb.Bucket
	lastT int64
}

// New 构造提交器。
func New(b *lb.Bucket) *Submitter {
	return &Submitter{b: b}
}

// Submit 校验 t≥0 且单调（失败不改任何状态），然后排水、判满接纳。
func (s *Submitter) Submit(t int64) (dep int64, admitted bool, err error) {
	if t < 0 {
		return 0, false, ErrNegativeT
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if t < s.lastT {
		return 0, false, ErrNonMonotonic
	}
	s.lastT = t
	s.b.Drain(t)
	dep, full := s.b.Admit(t)
	if full {
		return 0, false, nil
	}
	return dep, true, nil
}

// InSystem 返回系统内（未漏出）项数。
func (s *Submitter) InSystem() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.InSystem()
}
