// Package stat 提供调用统计、口径自洽检查，以及串联
// 熔断 → 舱壁 → 超时 → 分类 → 上报 的执行器。
package stat

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"ontology/breaker"
	"ontology/bulkhead"
	"ontology/classify"
	"ontology/timeout"
)

// Recorder 记录各口径计数器，并发安全。
type Recorder struct {
	total       atomic.Int64
	real        atomic.Int64
	breakerRej  atomic.Int64
	bulkheadRej atomic.Int64
	success     atomic.Int64
	fails       [classify.NumKinds]atomic.Int64
}

// Snapshot 是某一时刻各计数器的一致视图（逐字段读取的近似快照）。
type Snapshot struct {
	Total          int64
	Real           int64
	BreakerReject  int64
	BulkheadReject int64
	Success        int64
	Failed         int64
	ByKind         [classify.NumKinds]int64
}

// Snapshot 读取当前计数器。
func (r *Recorder) Snapshot() Snapshot {
	s := Snapshot{
		Total:          r.total.Load(),
		Real:           r.real.Load(),
		BreakerReject:  r.breakerRej.Load(),
		BulkheadReject: r.bulkheadRej.Load(),
		Success:        r.success.Load(),
	}
	for i := range s.ByKind {
		s.ByKind[i] = r.fails[i].Load()
		s.Failed += s.ByKind[i]
	}
	return s
}

// Violations 返回被违反的口径等式，空切片表示全部自洽。
func (s Snapshot) Violations() []string {
	var out []string
	if sum := s.Success + s.Failed + s.BreakerReject + s.BulkheadReject; sum != s.Total {
		out = append(out, fmt.Sprintf("总请求数 %d != 成功+失败+熔断拒绝+舱壁拒绝 %d", s.Total, sum))
	}
	if want := s.Total - s.BreakerReject - s.BulkheadReject; s.Real != want {
		out = append(out, fmt.Sprintf("真实调用数 %d != 总请求-两类拒绝 %d", s.Real, want))
	}
	var kindSum int64
	for _, n := range s.ByKind {
		kindSum += n
	}
	if kindSum != s.Failed {
		out = append(out, fmt.Sprintf("分类失败和 %d != 失败总数 %d", kindSum, s.Failed))
	}
	return out
}

// Executor 把一次调用依次穿过熔断、舱壁、超时，并记录全部口径。
type Executor struct {
	br      *breaker.Breaker
	bh      *bulkhead.Bulkhead
	timeout time.Duration
	rec     *Recorder
}

// NewExecutor 组装执行器；timeout <= 0 表示不做单次时限。
func NewExecutor(br *breaker.Breaker, bh *bulkhead.Bulkhead, timeout time.Duration, rec *Recorder) *Executor {
	return &Executor{br: br, bh: bh, timeout: timeout, rec: rec}
}

// Do 执行 fn：熔断在前，舱壁在后；只有真实调用的结果上报熔断器。
func (e *Executor) Do(ctx context.Context, fn func(context.Context) error) error {
	e.rec.total.Add(1)
	if err := e.br.Allow(); err != nil {
		e.rec.breakerRej.Add(1) // 含 ErrOpen 与时钟回拨拒绝
		return err
	}
	call := func() error {
		if e.timeout > 0 {
			return timeout.Do(ctx, e.timeout, fn)
		}
		return fn(ctx)
	}
	err := e.bh.Execute(ctx, call)
	if errors.Is(err, bulkhead.ErrRejected) || errors.Is(err, context.Canceled) {
		e.rec.bulkheadRej.Add(1) // 未触及下游：队列满或等待中被取消
		return err
	}
	e.rec.real.Add(1)
	if err == nil {
		e.rec.success.Add(1)
	} else {
		e.rec.fails[classify.Of(err)].Add(1)
	}
	e.br.Report(err)
	return err
}
