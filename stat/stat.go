// Package stat 记录调用保护器的统计计数，并提供口径自洽检查。
//
// 口径：每个请求在入口计入 Total，且恰好落入唯一终态——
// Success、Failure（按类别细分）、BreakerRejected 或 BulkheadRejected。
// 只有真正执行了被保护调用的请求才计入 Real 与 Success/Failure。
package stat

import (
	"fmt"
	"sync/atomic"

	"ontology/classify"
)

// Snapshot 是某一时刻各计数器的一致视图。
type Snapshot struct {
	Total              int64 // 进入保护器的请求总数
	Real               int64 // 真实调用数（真正执行了 fn）
	BreakerRejected    int64 // 被熔断拒绝数
	BulkheadRejected   int64 // 被舱壁拒绝数（含等待中被取消）
	Success            int64 // 成功数
	Failure            int64 // 失败数（各类别之和）
	FailureRetryable   int64 // 失败中可重试的部分
	FailureNonRetryable int64 // 失败中不可重试的部分
	FailureTimeout     int64 // 失败中超时的部分
}

// Invariants 返回被违反的口径等式描述；为空表示全部成立。
func (s Snapshot) Invariants() []string {
	var bad []string
	if s.Total != s.Success+s.Failure+s.BreakerRejected+s.BulkheadRejected {
		bad = append(bad, fmt.Sprintf("Total(%d) != Success+Failure+BreakerRejected+BulkheadRejected(%d)",
			s.Total, s.Success+s.Failure+s.BreakerRejected+s.BulkheadRejected))
	}
	if s.Real != s.Total-s.BreakerRejected-s.BulkheadRejected {
		bad = append(bad, fmt.Sprintf("Real(%d) != Total-BreakerRejected-BulkheadRejected(%d)",
			s.Real, s.Total-s.BreakerRejected-s.BulkheadRejected))
	}
	if s.Failure != s.FailureRetryable+s.FailureNonRetryable+s.FailureTimeout {
		bad = append(bad, fmt.Sprintf("Failure(%d) != 分类之和(%d)",
			s.Failure, s.FailureRetryable+s.FailureNonRetryable+s.FailureTimeout))
	}
	return bad
}

// Recorder 是并发安全的计数器集合。
type Recorder struct {
	total, real, breakerRejected, bulkheadRejected atomic.Int64
	success, failure                               atomic.Int64
	failureRetryable, failureNonRetryable          atomic.Int64
	failureTimeout                                 atomic.Int64
}

// RecordTotal 在请求进入保护器时调用。
func (r *Recorder) RecordTotal() { r.total.Add(1) }

// RecordReal 在请求真正开始执行被保护调用时调用。
func (r *Recorder) RecordReal() { r.real.Add(1) }

// RecordSuccess 在真实调用成功时调用。
func (r *Recorder) RecordSuccess() { r.success.Add(1) }

// RecordFailure 在真实调用失败时按类别调用。
func (r *Recorder) RecordFailure(k classify.Kind) {
	r.failure.Add(1)
	switch k {
	case classify.NonRetryable:
		r.failureNonRetryable.Add(1)
	case classify.Timeout:
		r.failureTimeout.Add(1)
	default:
		r.failureRetryable.Add(1)
	}
}

// RecordBreakerReject 在请求被熔断拒绝时调用。
func (r *Recorder) RecordBreakerReject() { r.breakerRejected.Add(1) }

// RecordBulkheadReject 在请求被舱壁拒绝（含等待中被取消）时调用。
func (r *Recorder) RecordBulkheadReject() { r.bulkheadRejected.Add(1) }

// Snapshot 返回当前各计数器的视图。
func (r *Recorder) Snapshot() Snapshot {
	return Snapshot{
		Total:               r.total.Load(),
		Real:                r.real.Load(),
		BreakerRejected:     r.breakerRejected.Load(),
		BulkheadRejected:    r.bulkheadRejected.Load(),
		Success:             r.success.Load(),
		Failure:             r.failure.Load(),
		FailureRetryable:    r.failureRetryable.Load(),
		FailureNonRetryable: r.failureNonRetryable.Load(),
		FailureTimeout:      r.failureTimeout.Load(),
	}
}
