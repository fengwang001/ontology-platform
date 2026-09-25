// Package stat 记录调用统计并校验口径自洽等式。
package stat

import (
	"fmt"
	"sync/atomic"

	"ontology/classify"
)

// Stat 并发安全的调用统计。
type Stat struct {
	total          atomic.Uint64
	real           atomic.Uint64
	rejectOpen     atomic.Uint64
	rejectBulkhead atomic.Uint64
	success        atomic.Uint64
	failRetryable  atomic.Uint64
	failNonRetry   atomic.Uint64
	failTimeout    atomic.Uint64
}

// AddSuccess 记录一次真实调用成功。
func (s *Stat) AddSuccess() {
	s.total.Add(1)
	s.real.Add(1)
	s.success.Add(1)
}

// AddFailure 按类别记录一次真实调用失败。
func (s *Stat) AddFailure(k classify.Kind) {
	s.total.Add(1)
	s.real.Add(1)
	switch k {
	case classify.NonRetryable:
		s.failNonRetry.Add(1)
	case classify.Timeout:
		s.failTimeout.Add(1)
	default:
		s.failRetryable.Add(1)
	}
}

// AddRejectOpen 记录一次熔断拒绝（不计入真实调用与失败率）。
func (s *Stat) AddRejectOpen() {
	s.total.Add(1)
	s.rejectOpen.Add(1)
}

// AddRejectBulkhead 记录一次舱壁拒绝（不计入真实调用与失败率）。
func (s *Stat) AddRejectBulkhead() {
	s.total.Add(1)
	s.rejectBulkhead.Add(1)
}

// Snapshot 是某一时刻的统计快照。
type Snapshot struct {
	Total            uint64
	Real             uint64
	RejectedOpen     uint64
	RejectedBulkhead uint64
	Success          uint64
	FailRetryable    uint64
	FailNonRetryable uint64
	FailTimeout      uint64
}

// Failure 返回失败总数。
func (s Snapshot) Failure() uint64 {
	return s.FailRetryable + s.FailNonRetryable + s.FailTimeout
}

// FailureRate 返回真实调用的失败率；无真实调用时为 0。
func (s Snapshot) FailureRate() float64 {
	if s.Real == 0 {
		return 0
	}
	return float64(s.Failure()) / float64(s.Real)
}

// Snapshot 读取一致口径的快照（各计数器独立原子读，等式由写口径保证）。
func (s *Stat) Snapshot() Snapshot {
	return Snapshot{
		Total:            s.total.Load(),
		Real:             s.real.Load(),
		RejectedOpen:     s.rejectOpen.Load(),
		RejectedBulkhead: s.rejectBulkhead.Load(),
		Success:          s.success.Load(),
		FailRetryable:    s.failRetryable.Load(),
		FailNonRetryable: s.failNonRetry.Load(),
		FailTimeout:      s.failTimeout.Load(),
	}
}

// Check 校验三条口径自洽等式，全部成立返回 nil。
func (s Snapshot) Check() error {
	if s.Total != s.Success+s.Failure()+s.RejectedOpen+s.RejectedBulkhead {
		return fmt.Errorf("total %d != success+failure+rejects %d",
			s.Total, s.Success+s.Failure()+s.RejectedOpen+s.RejectedBulkhead)
	}
	if s.Real != s.Total-s.RejectedOpen-s.RejectedBulkhead {
		return fmt.Errorf("real %d != total-rejects %d",
			s.Real, s.Total-s.RejectedOpen-s.RejectedBulkhead)
	}
	if s.Failure() != s.FailRetryable+s.FailNonRetryable+s.FailTimeout {
		return fmt.Errorf("failure sum mismatch")
	}
	return nil
}
