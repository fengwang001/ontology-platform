// Package stat 记录保护器的调用统计，全部计数原子操作，
// 并提供任意交错下的口径自洽检查（见 DESIGN.md 第 8 节）。
package stat

import (
	"sync/atomic"

	"ontology/classify"
)

// Stats 是单个保护器的并发安全计数器。
type Stats struct {
	total           atomic.Int64
	real            atomic.Int64
	rejectedBreaker atomic.Int64
	rejectedBulk    atomic.Int64
	success         atomic.Int64
	failedRetryable atomic.Int64
	failedNonRetr   atomic.Int64
	failedTimeout   atomic.Int64
	failedPanic     atomic.Int64
}

// Request 在每次请求进入时调用，计入总请求数。
func (s *Stats) Request() { s.total.Add(1) }

// RejectByBreaker 记录一次熔断拒绝（不算真实调用、不算失败）。
func (s *Stats) RejectByBreaker() { s.rejectedBreaker.Add(1) }

// RejectByBulkhead 记录一次舱壁拒绝（不算真实调用、不算失败）。
func (s *Stats) RejectByBulkhead() { s.rejectedBulk.Add(1) }

// Record 记录一次真实调用的结果。
func (s *Stats) Record(err error) {
	s.real.Add(1)
	if err == nil {
		s.success.Add(1)
		return
	}
	switch classify.Classify(err) {
	case classify.KindTimeout:
		s.failedTimeout.Add(1)
	case classify.KindPanic:
		s.failedPanic.Add(1)
	case classify.KindNonRetryable:
		s.failedNonRetr.Add(1)
	default:
		s.failedRetryable.Add(1)
	}
}

// Snapshot 是某一时刻的计数快照。
type Snapshot struct {
	Total, Real, RejectedByBreaker, RejectedByBulkhead, Success int64
	FailedRetryable, FailedNonRetryable, FailedTimeout, FailedPanic int64
}

// Snapshot 返回当前计数快照。
func (s *Stats) Snapshot() Snapshot {
	return Snapshot{
		Total:                  s.total.Load(),
		Real:                   s.real.Load(),
		RejectedByBreaker:      s.rejectedBreaker.Load(),
		RejectedByBulkhead:     s.rejectedBulk.Load(),
		Success:                s.success.Load(),
		FailedRetryable:        s.failedRetryable.Load(),
		FailedNonRetryable:     s.failedNonRetr.Load(),
		FailedTimeout:          s.failedTimeout.Load(),
		FailedPanic:            s.failedPanic.Load(),
	}
}

// Failed 返回失败总数（按类别细分之和）。
func (s Snapshot) Failed() int64 {
	return s.FailedRetryable + s.FailedNonRetryable + s.FailedTimeout + s.FailedPanic
}

// SelfConsistent 检查三条口径等式是否全部成立。
func (s Snapshot) SelfConsistent() bool {
	return s.Total == s.Success+s.Failed()+s.RejectedByBreaker+s.RejectedByBulkhead &&
		s.Real == s.Total-s.RejectedByBreaker-s.RejectedByBulkhead &&
		s.Real == s.Success+s.Failed()
}
