// Package stat 记录调用统计，并提供三条口径恒等式的自洽检查：
// 总请求数 = 成功 + 失败 + 熔断拒绝 + 舱壁拒绝；
// 真实调用数 = 总请求数 − 熔断拒绝 − 舱壁拒绝；
// 按类别细分的失败数之和 = 失败数。
package stat

import (
	"fmt"
	"sync/atomic"

	"ontology/classify"
)

// Counter 是并发安全的调用统计计数器，零值即可使用。
type Counter struct {
	total, real, breakerRej, bulkheadRej, success, failure atomic.Int64
	byKind                                                 [classify.NumKind]atomic.Int64
}

// RecordSuccess 记录一次成功的真实调用。
func (c *Counter) RecordSuccess() {
	c.total.Add(1)
	c.real.Add(1)
	c.success.Add(1)
}

// RecordFailure 记录一次失败的真实调用，并按类别细分。
func (c *Counter) RecordFailure(k classify.Kind) {
	c.total.Add(1)
	c.real.Add(1)
	c.failure.Add(1)
	c.byKind[k].Add(1)
}

// RecordBreakerReject 记录一次被熔断拒绝的请求（非真实调用）。
func (c *Counter) RecordBreakerReject() {
	c.total.Add(1)
	c.breakerRej.Add(1)
}

// RecordBulkheadReject 记录一次被舱壁拒绝的请求（非真实调用）。
func (c *Counter) RecordBulkheadReject() {
	c.total.Add(1)
	c.bulkheadRej.Add(1)
}

// Snapshot 是计数器在某时刻的一致性视图。
type Snapshot struct {
	Total            int64
	Real             int64
	BreakerRejected  int64
	BulkheadRejected int64
	Success          int64
	Failure          int64
	ByKind           [classify.NumKind]int64
}

// Snapshot 读取当前快照。
func (c *Counter) Snapshot() Snapshot {
	var s Snapshot
	s.Total = c.total.Load()
	s.Real = c.real.Load()
	s.BreakerRejected = c.breakerRej.Load()
	s.BulkheadRejected = c.bulkheadRej.Load()
	s.Success = c.success.Load()
	s.Failure = c.failure.Load()
	for i := range s.ByKind {
		s.ByKind[i] = c.byKind[i].Load()
	}
	return s
}

// Check 校验三条恒等式，返回第一处违例；全部成立时返回 nil。
func (s Snapshot) Check() error {
	if sum := s.Success + s.Failure + s.BreakerRejected + s.BulkheadRejected; s.Total != sum {
		return fmt.Errorf("stat: total %d != success+failure+rejects %d", s.Total, sum)
	}
	if want := s.Total - s.BreakerRejected - s.BulkheadRejected; s.Real != want {
		return fmt.Errorf("stat: real %d != total-rejects %d", s.Real, want)
	}
	if sum := s.ByKind[0] + s.ByKind[1] + s.ByKind[2]; sum != s.Failure {
		return fmt.Errorf("stat: failure %d != sum of kinds %d", s.Failure, sum)
	}
	return nil
}
