// Package ewma 实现峰值抬升、线性衰减的时延估计器。
//
// 每个端点保存 (est, last, hasSample)。读值时无样本返回先验 P0，
// 否则按 d=min(now-last, τ) 线性衰减：val = est*(τ-d)/τ（整数运算）。
// 样本更新时 est = max(rtt, 当前衰减值)，保证峰值立即抬升、只减不增地衰减。
package ewma

// Estimator 是单个端点的峰值衰减时延估计器。零值表示无样本。
// 不是并发安全的，由上层（picker 的单锁）串行化访问。
type Estimator struct {
	est  int64
	last int64
	has  bool
}

// Val 返回 now 时刻的时延估计。无样本时返回 p0。
// 调用方保证 now >= 最近一次 Update 的 now（上层已做时钟回退检查）。
func (e *Estimator) Val(now, tau, p0 int64) int64 {
	if !e.has {
		return p0
	}
	d := now - e.last
	if d > tau {
		d = tau
	}
	return e.est * (tau - d) / tau
}

// Update 以 rtt 为样本更新估计：无样本则 est=rtt，
// 否则 est=max(rtt, 当前衰减值)，并令 last=now。
func (e *Estimator) Update(rtt, now, tau int64) {
	if !e.has {
		e.est = rtt
	} else if v := e.Val(now, tau, 0); rtt > v {
		e.est = rtt
	} else {
		e.est = v
	}
	e.last = now
	e.has = true
}

// HasSample 报告是否已有样本。
func (e *Estimator) HasSample() bool { return e.has }

// Raw 返回内部状态，仅供测试与对照模拟读取。
func (e *Estimator) Raw() (est, last int64, has bool) { return e.est, e.last, e.has }
