// Package naivemodel 是与 demand.Controller 独立编写的朴素参考实现：
// 保留全部上报历史，评估时对每个活跃窗口逐秒重算用电量，
// 不使用滑窗/环缓冲技巧，用于随机序列对照。
package naivemodel

import (
	"math/big"
	"sort"
)

type Config struct {
	ContractKW int64
	WindowSec  int64
	SlipSec    int64
	MaxPowerKW int64
}

type LoadSpec struct {
	ID        int
	RatedKW   int64
	Priority  int
	MinOnSec  int64
	MinOffSec int64
}

type load struct {
	spec   LoadSpec
	on     bool
	locked bool
	since  int64
}

// seg 为恒定功率区间 (l,r]。
type seg struct {
	l, r  int64
	power *big.Rat
}

const (
	AKindShed    = 1
	AKindRestore = 2
)

type Action struct {
	Kind   int
	LoadID int
	At     int64
}

type Result struct {
	At          int64
	Actions     []Action
	StillExceed bool
}

type Peak struct {
	PowerKW *big.Rat
	EndAt   int64
}

type ErrKind int

const (
	EParam ErrKind = iota + 1
	EData
	ERewind
	ENotFound
	EState
)

type MError struct{ Kind ErrKind }

func (e *MError) Error() string { return "naive error" }

type Model struct {
	cfg   Config
	loads map[int]*load

	history []seg
	lastR   int64
	now     int64

	closedTo int64 // 已关闭窗口的最大结束时刻；0 表示尚无窗口关闭
	peak     *Peak
}

func New(cfg Config) *Model {
	return &Model{cfg: cfg, loads: map[int]*load{}, lastR: -1, now: -1, closedTo: 0}
}

func validSpec(s LoadSpec) bool {
	return s.ID > 0 && s.RatedKW > 0 && s.Priority > 0 && s.MinOnSec >= 0 && s.MinOffSec >= 0
}

func (m *Model) AddLoad(at int64, s LoadSpec) error {
	if at < 0 || !validSpec(s) {
		return &MError{Kind: EParam}
	}
	if _, ok := m.loads[s.ID]; ok {
		return &MError{Kind: EParam}
	}
	if at < m.now {
		return &MError{Kind: EState}
	}
	if at > m.now {
		m.now = at
	}
	m.loads[s.ID] = &load{spec: s, on: true, since: at}
	return nil
}

func (m *Model) LockLoad(at int64, id int) error {
	if at < 0 {
		return &MError{Kind: EParam}
	}
	l, ok := m.loads[id]
	if !ok {
		return &MError{Kind: ENotFound}
	}
	if at < m.now {
		return &MError{Kind: EState}
	}
	if l.locked {
		return &MError{Kind: EState}
	}
	if at > m.now {
		m.now = at
	}
	l.locked = true
	return nil
}

func (m *Model) UnlockLoad(at int64, id int) error {
	if at < 0 {
		return &MError{Kind: EParam}
	}
	l, ok := m.loads[id]
	if !ok {
		return &MError{Kind: ENotFound}
	}
	if at < m.now {
		return &MError{Kind: EState}
	}
	if !l.locked {
		return &MError{Kind: EState}
	}
	if at > m.now {
		m.now = at
	}
	l.locked = false
	return nil
}

func (m *Model) RemoveLoad(at int64, id int) error {
	if at < 0 {
		return &MError{Kind: EParam}
	}
	if _, ok := m.loads[id]; !ok {
		return &MError{Kind: ENotFound}
	}
	return &MError{Kind: EState}
}

// Report 朴素保留全部区间。
func (m *Model) Report(t, energy int64) (*Result, error) {
	if t < 0 {
		return nil, &MError{Kind: EParam}
	}
	if energy < 0 {
		return nil, &MError{Kind: EData}
	}
	prev := m.lastR
	if prev >= 0 && t <= prev {
		return nil, &MError{Kind: ERewind}
	}
	if prev >= 0 {
		if energy > m.cfg.MaxPowerKW*(t-prev) {
			return nil, &MError{Kind: EData}
		}
	} else if t > 0 && energy > m.cfg.MaxPowerKW*t {
		return nil, &MError{Kind: EData}
	}

	var p *big.Rat
	if prev < 0 {
		if t > 0 {
			p = big.NewRat(energy, t)
		} else {
			p = big.NewRat(0, 1)
		}
		m.history = append(m.history, seg{l: 0, r: t, power: p})
	} else {
		p = big.NewRat(energy, t-prev)
		m.history = append(m.history, seg{l: prev, r: t, power: p})
	}
	m.lastR = t
	if t > m.now {
		m.now = t
	}

	// 与控制器一致：先按新时刻关窗（结束于 t 的窗口此刻到期），再追加区间并评估。
	m.closeWindows(t)
	return m.evaluate(t), nil
}

func (m *Model) closeWindows(now int64) {
	// 窗口只在滑差整数倍的绝对时刻结束；逐个关闭 end <= now 的窗口。
	slip := m.cfg.SlipSec
	next := m.closedTo + slip
	if m.closedTo == 0 {
		next = slip
	}
	for end := next; end <= now; end += slip {
		e := m.windowEnergy(end)
		power := e.clone().divInt(m.cfg.WindowSec)
		if m.peak == nil || power.cmpRat(m.peak.PowerKW) > 0 {
			m.peak = &Peak{PowerKW: power.toRat(), EndAt: end}
		}
		m.closedTo = end
	}
}

// energyIn 返回上报口径下 (a,b] 区间的用电量。
// 被接受区间首尾相接、区间内功率恒定，直接对各区间求交并按重叠长度积分。
func (m *Model) energyIn(a, b int64) *ratio {
	total := newRat()
	for _, s := range m.history {
		lo := s.l
		if a > lo {
			lo = a
		}
		hi := s.r
		if b < hi {
			hi = b
		}
		if hi > lo {
			total.add(newRat().set(s.power).mulInt(hi - lo))
		}
	}
	return total
}

// windowEnergy 计算窗口 [end-W, end) 内截至 end 的实测用电量
// （窗口结束时调用；区间不会超出已入库历史，超出部分天然为零）。
func (m *Model) windowEnergy(end int64) *ratio {
	start := end - m.cfg.WindowSec
	return m.energyIn(start, end)
}

func (m *Model) Peak() *Peak {
	if m.peak == nil {
		return nil
	}
	return &Peak{PowerKW: new(big.Rat).Set(m.peak.PowerKW), EndAt: m.peak.EndAt}
}

func sortActions(as []Action) {
	sort.Slice(as, func(i, j int) bool {
		if as[i].Kind != as[j].Kind {
			return as[i].Kind < as[j].Kind
		}
		return as[i].LoadID < as[j].LoadID
	})
}
