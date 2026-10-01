// Package ontology 实现带翻转抑制与全局快速探测名额的主动健康检查状态机。
package ontology

import (
	"errors"
	"sync"
)

// Probe 拒绝原因，按规定顺序只报第一个。
var (
	ErrInvalidConfig      = errors.New("invalid health checker config")
	ErrTargetOutOfRange   = errors.New("target id out of range")
	ErrInvalidTime        = errors.New("invalid now: must be in [0, 1e15]")
	ErrClockWentBackwards = errors.New("clock went backwards: now smaller than accepted maximum")
	ErrProbeTooEarly      = errors.New("probe too early: now before next-due time")
)

const (
	maxInterval = 1_000_000_000
	maxNow      = 1_000_000_000_000_000
)

// TargetState 是单个目标在某一时刻的可观测状态快照。
type TargetState struct {
	Healthy bool
	A       int64 // 不健康态下连续成功数
	B       int64 // 健康态下连续失败数
	ND      int64 // 下次探测最早时刻
	FU      int64 // 加速（快速探测）截止时刻
	TR      []int64
}

// HealthChecker 是并发安全的主动健康检查状态机。
type HealthChecker struct {
	mu sync.Mutex

	n  int64
	r  int64
	f  int64
	i  int64
	fi int64
	di int64
	wf int64
	q  int64

	maxNow int64

	targets []target
}

type target struct {
	healthy bool
	a       int64 // 不健康态：连续成功数
	b       int64 // 健康态：连续失败数
	nd      int64 // 下次探测最早时刻
	fu      int64 // 快速名额占用截止时刻
	tr      []int64
}

// NewHealthChecker 创建状态机。配置非法时返回 ErrInvalidConfig。
func NewHealthChecker(n, r, f, i, fi, di int64, initialHealthy bool, wf int64, q int64) (*HealthChecker, error) {
	validInterval := func(v int64) bool { return v >= 1 && v <= maxInterval }
	if n < 1 || r < 1 || r > 1000 || f < 1 ||
		!validInterval(i) || !validInterval(fi) || !validInterval(di) ||
		wf < 1 || wf > maxInterval || q < 0 || q > n {
		return nil, ErrInvalidConfig
	}

	hc := &HealthChecker{
		n:       n,
		r:       r,
		f:       f,
		i:       i,
		fi:      fi,
		di:      di,
		wf:      wf,
		q:       q,
		maxNow:  0,
		targets: make([]target, n),
	}
	for idx := range hc.targets {
		hc.targets[idx].healthy = initialHealthy
		hc.targets[idx].tr = []int64{}
	}
	return hc, nil
}

// Probe 处理一次探测结果。
func (hc *HealthChecker) Probe(target int, ok bool, now int64) error {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	// 拒绝检查：按规定顺序只报第一个。
	if target < 0 || int64(target) >= hc.n {
		return ErrTargetOutOfRange
	}
	if now < 0 || now > maxNow {
		return ErrInvalidTime
	}
	if now < hc.maxNow {
		return ErrClockWentBackwards
	}
	tgt := &hc.targets[target]
	if now < tgt.nd {
		return ErrProbeTooEarly
	}

	// 计数与转移。
	if tgt.healthy {
		if ok {
			tgt.b = 0
		} else {
			tgt.b++
			if tgt.b >= hc.f {
				tgt.healthy = false
				tgt.a = 0
				tgt.b = 0
				tgt.tr = append(tgt.tr, now)
			}
		}
	} else {
		if !ok {
			tgt.a = 0
		} else {
			tgt.a++
			g := hc.activeTransitions(tgt, now)
			rEff := hc.r * (1 + min(g, 4))
			if tgt.a >= rEff {
				tgt.healthy = true
				tgt.a = 0
				tgt.b = 0
				tgt.tr = append(tgt.tr, now)
			}
		}
	}

	// 间隔选择。
	var interval int64
	fastCandidate := false
	switch {
	case tgt.healthy && tgt.b > 0:
		interval, fastCandidate = hc.fi, true
	case tgt.healthy && tgt.b == 0:
		interval = hc.i
	case !tgt.healthy && tgt.a > 0:
		interval, fastCandidate = hc.fi, true
	default: // 不健康且 a == 0
		interval = hc.di
	}

	if fastCandidate {
		x := int64(0)
		for idx := range hc.targets {
			if idx == target {
				continue
			}
			if hc.targets[idx].fu > now {
				x++
			}
		}
		if x >= hc.q {
			// 名额不足，改取当前状态的常规间隔。
			if tgt.healthy {
				interval = hc.i
			} else {
				interval = hc.di
			}
			tgt.fu = 0
		} else {
			interval = hc.fi
			tgt.fu = now + hc.fi
		}
	} else {
		tgt.fu = 0
	}

	tgt.nd = now + interval
	if now > hc.maxNow {
		hc.maxNow = now
	}
	return nil
}

// State 返回目标状态快照（TR 为拷贝）。
func (hc *HealthChecker) State(target int) (TargetState, error) {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	if target < 0 || int64(target) >= hc.n {
		return TargetState{}, ErrTargetOutOfRange
	}
	tgt := &hc.targets[target]
	trCopy := make([]int64, len(tgt.tr))
	copy(trCopy, tgt.tr)
	return TargetState{
		Healthy: tgt.healthy,
		A:       tgt.a,
		B:       tgt.b,
		ND:      tgt.nd,
		FU:      tgt.fu,
		TR:      trCopy,
	}, nil
}

// Healthy 返回当前健康目标编号的升序列表。
func (hc *HealthChecker) Healthy() []int {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	healthy := make([]int, 0)
	for idx := range hc.targets {
		if hc.targets[idx].healthy {
			healthy = append(healthy, idx)
		}
	}
	return healthy
}

// activeTransitions 统计 now 时刻目标转移历史中仍然有效的条目数。
// t+Wf > now 有效；t+Wf == now 恰好过期。
func (hc *HealthChecker) activeTransitions(tgt *target, now int64) int64 {
	g := int64(0)
	for _, t := range tgt.tr {
		if t+hc.wf > now {
			g++
		}
	}
	return g
}
