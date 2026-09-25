// Package stat 记录调用保护器的各项计数，并提供口径自洽检查。
// 每个请求恰好落账一次：被熔断拒绝、被舱壁拒绝、或作为真实调用
// 以成功 / 失败（按类别细分）收尾。
package stat

import (
	"sync"

	"ontology/classify"
)

// Snapshot 是某一时刻的计数快照。
type Snapshot struct {
	Total            int64 // 总请求数
	Real             int64 // 真实调用数（通过熔断与舱壁两关）
	BreakerRejected  int64 // 被熔断拒绝数
	BulkheadRejected int64 // 被舱壁拒绝数
	Success          int64 // 成功数
	Failure          int64 // 失败数
	Retryable        int64 // 失败细分：可重试
	NonRetryable     int64 // 失败细分：不可重试
	Timeout          int64 // 失败细分：超时
}

// Consistent 检查三条口径等式是否同时成立。
func (s Snapshot) Consistent() bool {
	return s.Total == s.Success+s.Failure+s.BreakerRejected+s.BulkheadRejected &&
		s.Real == s.Total-s.BreakerRejected-s.BulkheadRejected &&
		s.Real == s.Success+s.Failure &&
		s.Failure == s.Retryable+s.NonRetryable+s.Timeout
}

// Stat 是并发安全的计数器集合。
type Stat struct {
	mu   sync.Mutex
	snap Snapshot
}

// RecordSuccess 记录一次真实调用成功。
func (s *Stat) RecordSuccess() {
	s.mu.Lock()
	s.snap.Total++
	s.snap.Real++
	s.snap.Success++
	s.mu.Unlock()
}

// RecordFailure 按类别记录一次真实调用失败。
func (s *Stat) RecordFailure(k classify.Kind) {
	s.mu.Lock()
	s.snap.Total++
	s.snap.Real++
	s.snap.Failure++
	switch k {
	case classify.KindNonRetryable:
		s.snap.NonRetryable++
	case classify.KindTimeout:
		s.snap.Timeout++
	default:
		s.snap.Retryable++
	}
	s.mu.Unlock()
}

// RecordBreakerReject 记录一次熔断拒绝（不计入失败率）。
func (s *Stat) RecordBreakerReject() {
	s.mu.Lock()
	s.snap.Total++
	s.snap.BreakerRejected++
	s.mu.Unlock()
}

// RecordBulkheadReject 记录一次舱壁拒绝（不计入失败率）。
func (s *Stat) RecordBulkheadReject() {
	s.mu.Lock()
	s.snap.Total++
	s.snap.BulkheadRejected++
	s.mu.Unlock()
}

// Snapshot 返回当前计数快照。
func (s *Stat) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snap
}
