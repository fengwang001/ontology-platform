package filter

import "ontology/profile"

// Sample 是最近一个有效样本。
type Sample struct{ At, V int64 }

// State 是单个测点的全部可变运行状态。事件时刻由本状态与当前参数纯函数决定。
type State struct {
	lastV, lastAt int64
	hasLast       bool
	cur           Sample
	hasCur        bool
	bad           int64
	fault         bool
	deferUntil    int64
}

func NewState() *State { return &State{} }

func max3(a, b, c int64) int64 {
	if b > a {
		a = b
	}
	if c > a {
		a = c
	}
	return a
}

func absDiff(a, b int64) int64 {
	d := a - b
	if d < 0 {
		return -d
	}
	return d
}

// PendingAt 返回待报事件时刻：非故障、有 cur、cur.ts 晚于上次上报，且尚无 lastV
// 或相对 lastV 严格越过死区。
func (s *State) PendingAt(p profile.Params) (int64, bool) {
	if s.fault || !s.hasCur {
		return 0, false
	}
	if s.hasLast {
		if s.cur.At <= s.lastAt || absDiff(s.cur.V, s.lastV) <= p.DB {
			return 0, false
		}
		return max3(s.lastAt+p.MinInterval, s.cur.At, s.deferUntil), true
	}
	if s.cur.At < s.deferUntil {
		return s.deferUntil, true
	}
	return s.cur.At, true
}

// HeartbeatAt 返回心跳事件时刻：非故障且已有上次上报。
func (s *State) HeartbeatAt(p profile.Params) (int64, bool) {
	if s.fault || !s.hasLast {
		return 0, false
	}
	at := s.lastAt + p.MaxInterval
	if s.deferUntil > at {
		at = s.deferUntil
	}
	return at, true
}

// Ingest 登记一个样本。valid 样本清除 bad 并替换 cur；若此前处于故障则解除故障、
// 复位上报基准并返回 recover=true。invalid 样本累加 bad，恰达到 q 且此前非故障时
// 进入故障并返回 faulted=true；故障期间 cur 不变。
func (s *State) Ingest(t, v int64, p profile.Params, q int64) (recover, faulted bool) {
	if p.Valid(v) {
		s.bad = 0
		s.cur = Sample{At: t, V: v}
		s.hasCur = true
		if s.fault {
			s.fault = false
			s.lastV = v
			s.lastAt = t
			s.hasLast = true
			s.deferUntil = 0
			return true, false
		}
		return false, false
	}
	s.bad++
	if s.bad == q && !s.fault {
		s.fault = true
		return false, true
	}
	return false, false
}

// Report 记录一次成功发出的四类常规上报：更新死区基准与推迟下限。
func (s *State) Report(at, v int64) {
	s.lastV = v
	s.lastAt = at
	s.hasLast = true
	s.deferUntil = 0
}

// Throttle 在窗口额度用尽时把事件推迟到下一窗口起点。
func (s *State) Throttle(until int64) { s.deferUntil = until }

// CurV 返回最近有效样本值（待报/心跳均以它为上报值）。
func (s *State) CurV() int64 { return s.cur.V }

// CurAt 返回最近有效样本时刻，用于区分 Change 与 Trailing。
func (s *State) CurAt() int64 { return s.cur.At }

// Fault 报告当前是否处于故障态。
func (s *State) Fault() bool { return s.fault }

// DeferUntil 暴露当前推迟下限，供检查与测试。
func (s *State) DeferUntil() int64 { return s.deferUntil }

// LastAt 返回最近上报时刻及是否存在。
func (s *State) LastAt() (int64, bool) { return s.lastAt, s.hasLast }

// HasLast 报告是否已存在上报基准（First 与其余原因的分界）。
func (s *State) HasLast() bool { return s.hasLast }
