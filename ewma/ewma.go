// Package ewma 维护单个端点的峰值衰减时延估计。
//
// 估计量无样本时读先验 P0；有样本时，自上次采样起经过 d（被 τ 夹取），
// 估值按 val = floor(est*(τ-d)/τ) 线性衰减。新样本做峰值抬升：
// est = max(rtt, val)，并把 last 推进到 now。
package ewma

// Estimator 是单端点时延估计器。零值不可用，必须用 New 构造。
type Estimator struct {
	tau       int64
	prior     int64
	est       int64
	last      int64
	hasSample bool
}

// New 创建估计器。tau 为衰减期（毫秒），prior 为先验时延 P0。
func New(tau, prior int64) *Estimator {
	return &Estimator{tau: tau, prior: prior}
}

// Value 返回 now 时刻的读值。无样本返回先验 P0。
func (e *Estimator) Value(now int64) int64 {
	if !e.hasSample {
		return e.prior
	}
	d := now - e.last
	if d > e.tau {
		d = e.tau
	}
	return e.est * (e.tau - d) / e.tau
}

// Sample 在 now 时刻写入一个时延样本 rtt：
// 无样本则 est=rtt，否则 est=max(rtt, 当前衰减值)；随后 last=now。
func (e *Estimator) Sample(rtt, now int64) {
	if !e.hasSample {
		e.est = rtt
	} else if v := e.Value(now); rtt > v {
		e.est = rtt
	} else {
		e.est = v
	}
	e.last = now
	e.hasSample = true
}
