package rollout

// 本文件是与实现完全独立的“朴素参考模型”，仅用于随机差分测试。
// 它刻意用最直白的方式重新实现规范，不与生产代码共享任何内部结构
//（只复用类型、配置与错误哨兵，以便逐条比较返回值）。

import (
	"hash/fnv"
	"sort"
)

type naiveSticky struct {
	id      string
	version Version
	atMs    int64
	seq     uint64
}

type naiveModel struct {
	cfg    Config
	phase  Phase
	idx    int
	enter  int64
	fails  int
	win    window
	stick  []naiveSticky // 朴素地用切片保存；seq 最大者为最近路由
	seq    uint64
	hasClk bool
	lastMs int64
}

func naivePosition(id string) int {
	h := fnv.New64a()
	_, _ = h.Write([]byte(id))
	return int(h.Sum64() % 10000)
}

func newNaive(cfg Config) *naiveModel {
	return &naiveModel{cfg: cfg}
}

// ordered 返回按“最近路由在前”排序的粘性副本，seq 大的在前；
// seq 相同（不会发生）时按 id 兜底保证确定序。
func (m *naiveModel) ordered() []naiveSticky {
	out := append([]naiveSticky(nil), m.stick...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].seq != out[j].seq {
			return out[i].seq > out[j].seq
		}
		return out[i].id < out[j].id
	})
	return out
}

func (m *naiveModel) ratio() int {
	switch m.phase {
	case PhaseRunning:
		return m.cfg.Ratios[m.idx]
	case PhaseCompleted:
		return m.cfg.Ratios[len(m.cfg.Ratios)-1]
	default:
		return 0
	}
}

func (m *naiveModel) checkClock(now int64) error {
	if m.hasClk && now < m.lastMs {
		return ErrClockRewound
	}
	return nil
}

func (m *naiveModel) tick(now int64) {
	if !m.hasClk || now > m.lastMs {
		m.lastMs = now
	}
	m.hasClk = true
}

func (m *naiveModel) start(now int64) error {
	if err := m.checkClock(now); err != nil {
		return err
	}
	if m.phase != PhaseNotStarted {
		return ErrIllegalState
	}
	m.tick(now)
	m.phase = PhaseRunning
	m.idx = 0
	m.enter = now
	m.fails = 0
	m.win = window{}
	return nil
}

func (m *naiveModel) reset() {
	*m = *newNaive(m.cfg)
}

func (m *naiveModel) downgrade(now int64) error {
	if err := m.checkClock(now); err != nil {
		return err
	}
	if m.phase != PhaseRunning || m.idx == 0 {
		return ErrIllegalState
	}
	m.tick(now)
	m.idx--
	m.enter = now
	m.fails = 0
	m.win = window{}
	return nil
}

func (m *naiveModel) route(id string, now int64) (RouteDecision, error) {
	if err := m.checkClock(now); err != nil {
		return RouteDecision{}, err
	}
	m.tick(now)
	if m.phase == PhaseRolledBack {
		return RouteDecision{VersionStable, SourceForceStable}, nil
	}
	// 线性扫描找未过期粘性。
	for i := range m.stick {
		e := &m.stick[i]
		if e.id == id && now >= e.atMs && now-e.atMs < m.cfg.StickyMs {
			m.seq++
			e.atMs = now
			e.seq = m.seq
			return RouteDecision{e.version, SourceSticky}, nil
		}
	}
	v := VersionStable
	if naivePosition(id) < m.ratio() {
		v = VersionCanary
	}
	if m.cfg.MaxSticky > 0 {
		m.seq++
		// 覆盖同 id（含已过期）记录。
		for i := range m.stick {
			if m.stick[i].id == id {
				m.stick[i] = naiveSticky{id, v, now, m.seq}
				return RouteDecision{v, SourceProportion}, nil
			}
		}
		m.stick = append(m.stick, naiveSticky{id, v, now, m.seq})
		if len(m.stick) > m.cfg.MaxSticky {
			ord := m.ordered()
			drop := ord[len(ord)-1]
			kept := m.stick[:0]
			for _, e := range m.stick {
				if e.id != drop.id || e.seq != drop.seq {
					kept = append(kept, e)
				}
			}
			m.stick = kept
		}
	}
	return RouteDecision{v, SourceProportion}, nil
}

func (m *naiveModel) observe(v Version, success bool, now int64) error {
	if v != VersionStable && v != VersionCanary {
		return ErrInvalidArgument
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	m.tick(now)
	if m.phase != PhaseRunning {
		return nil
	}
	if v == VersionCanary {
		m.win.canaryTotal++
		if !success {
			m.win.canaryFailed++
		}
	} else {
		m.win.stableTotal++
		if !success {
			m.win.stableFailed++
		}
	}
	return nil
}

func fracLE(gc, gn, sc, sn int64, tol int) bool {
	// gc/gn <= (sc/sn if sn>0 else 0) + tol/10000，朴素浮点先转整型分数比较。
	// 用交叉相乘的 int64：测试计数规模小，不会溢出。
	lhsNum, lhsDen := gc*10000, gn*10000
	rhsNum := int64(tol)
	rhsDen := int64(10000)
	if sn > 0 {
		// sc/sn + tol/10000 = (sc*10000 + tol*sn) / (sn*10000)
		rhsNum = sc*10000 + int64(tol)*sn
		rhsDen = sn * 10000
	}
	_ = lhsDen
	return lhsNum*rhsDen <= rhsNum*lhsDen
}

func (m *naiveModel) evaluate(now int64) (EvalResult, error) {
	if err := m.checkClock(now); err != nil {
		return EvalDwellNotMet, err
	}
	m.tick(now)
	if m.phase != PhaseRunning || now-m.enter < m.cfg.DwellMs {
		return EvalDwellNotMet, nil
	}
	if m.win.canaryTotal < int64(m.cfg.MinCanary) {
		return EvalInsufficient, nil
	}
	ok := fracLE(m.win.canaryFailed, m.win.canaryTotal,
		m.win.stableFailed, m.win.stableTotal, m.cfg.ToleranceBP)
	if !ok {
		m.fails++
		if m.fails >= m.cfg.MaxFailures {
			m.phase = PhaseRolledBack
			m.idx = 0
			m.enter = 0
			m.fails = 0
			m.win = window{}
			m.stick = nil
			return EvalFailed, nil
		}
		m.win = window{}
		m.enter = now
		return EvalFailed, nil
	}
	m.fails = 0
	m.win = window{}
	if m.idx >= len(m.cfg.Ratios)-1 {
		m.phase = PhaseCompleted
		m.idx = 0
		m.enter = 0
		return EvalPassed, nil
	}
	m.idx++
	m.enter = now
	return EvalPassed, nil
}

// state 比较用视图：粘性集合（id -> version, atMs, seq 排位无关，
// 比较时按 id 排序且忽略 seq 绝对值，只比较 seq 的相对先后）。
type naiveState struct {
	phase  Phase
	idx    int
	ratio  int
	enter  int64
	fails  int
	win    window
	lastMs int64
	hasClk bool
	sticky []naiveSticky
}

func (m *naiveModel) state() naiveState {
	st := naiveState{
		phase:  m.phase,
		idx:    m.idx,
		ratio:  m.ratio(),
		enter:  m.enter,
		fails:  m.fails,
		win:    m.win,
		lastMs: m.lastMs,
		hasClk: m.hasClk,
		sticky: m.ordered(),
	}
	return st
}
