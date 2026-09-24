// Package drift 维护单个源的水位线状态与阈值边界判定。
// 它不依赖本模块的其他任何包。
package drift

import "strconv"

// Class 是一条水位线观测的互斥分类。
type Class uint8

const (
	Normal   Class = iota // 正常前进（含恰等于 last、恰落在漂移边界上）
	Drift                 // 前进过猛：w > last + driftThreshold
	Reorder               // 小幅回退、可容忍：last-w <= rollbackTolerance
	Rollback              // 大幅回退，疑似时钟回拨：last-w > rollbackTolerance
)

func (c Class) String() string {
	switch c {
	case Normal:
		return "Normal"
	case Drift:
		return "Drift"
	case Reorder:
		return "Reorder"
	case Rollback:
		return "Rollback"
	default:
		return "Class(" + strconv.Itoa(int(c)) + ")"
	}
}

// State 是单个源的状态：最后接受的水位线 last 与两个阈值。
// 零值不可用，必须经 NewState 构造。
type State struct {
	last              int64
	seen              bool
	driftThreshold    int64
	rollbackTolerance int64
}

// NewState 创建单源状态。阈值合法性由上层（api.New）统一校验，
// 这里假定 driftThreshold > 0 且 rollbackTolerance >= 0。
func NewState(driftThreshold, rollbackTolerance int64) *State {
	return &State{
		driftThreshold:    driftThreshold,
		rollbackTolerance: rollbackTolerance,
	}
}

// Last 返回最后接受的水位线；第二个返回值报告该源是否已收到过水位线。
func (s *State) Last() (int64, bool) {
	return s.last, s.seen
}

// Observe 按当前 last 对一条水位线判类并推进状态。
// 规则（顺序即优先级，互斥）：
//   - 首条水位线无基线：直接接受为 last，判 Normal；
//   - w-last > driftThreshold：Drift，接受 last=w；
//   - w >= last（含等于；含 w-last == driftThreshold 的边界）：Normal，接受 last=w；
//   - w < last：回退幅度 d=last-w，d <= rollbackTolerance 为 Reorder，
//     否则 Rollback；两种回退都不修改 last（last 只进不退）。
func (s *State) Observe(w int64) Class {
	if !s.seen {
		s.last = w
		s.seen = true
		return Normal
	}
	if w > s.last {
		// 写成 w-last 而非 last+threshold，避免大数相加溢出 int64。
		if w-s.last > s.driftThreshold {
			s.last = w
			return Drift
		}
		s.last = w
		return Normal
	}
	if w == s.last {
		return Normal
	}
	d := s.last - w
	if d <= s.rollbackTolerance {
		return Reorder
	}
	return Rollback
}
