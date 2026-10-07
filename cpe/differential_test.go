package cpe

// 朴素模型：独立编写的参照实现。采用事件溯源风格——保留全部被接受事件，
// 每次查询都从头重放并按需全量扫描记录求和（不使用增量聚合与快照），
// 核算数学也独立重写，用于与主实现做差分对照。

import (
	"fmt"
)

func nCount(m, e, o, mCap, eCap int) (int, int, int, int) {
	if m > mCap {
		m = mCap
	}
	if e > eCap {
		e = eCap
	}
	if o > m+e {
		o = m + e
	}
	return m, e, o, m + e + o
}

func nPass(cm, tot int, cfg Config) bool {
	return tot >= cfg.TotalRequired && cm >= cfg.MandatoryMin
}

func nCarry(cm, ce, co, tot int, cfg Config) int {
	excess := tot - cfg.TotalRequired
	if excess <= 0 {
		return 0
	}
	left := cfg.TotalRequired - cm
	if left < 0 {
		left = 0
	}
	nx := ce + co - left
	if nx < 0 {
		nx = 0
	}
	c := excess
	if nx < c {
		c = nx
	}
	if c > cfg.CarryoverCap {
		c = cfg.CarryoverCap
	}
	return c
}

type nEvent struct {
	op         string // "reg" / "correct" / "revoke"
	now        int
	recID      string
	cat        Category
	credits    int
	earned     int
	org        string
	newCredits int
}

type nRec struct {
	cat     Category
	credits int
	earned  int
	org     string
	regNow  int
	revoked bool
	cycle   int
}

type nCyc struct {
	index, start, end int
	grace             bool
	graceEntered      bool
	carryIn           int
	closed            bool
	passed            bool
	passedInGrace     bool
	fRaw              Tally
	fCounted          Counted
	fCarryOut         int
}

type nState struct {
	status  CertStatus
	closed  []nCyc
	cur     nCyc
	hasCur  bool
	records map[string]*nRec
}

func (st *nState) tallyOf(idx int) (int, int, int) {
	m, e, o := 0, 0, 0
	for _, r := range st.records {
		if r.cycle != idx || r.revoked {
			continue
		}
		switch r.cat {
		case Mandatory:
			m += r.credits
		case Elective:
			e += r.credits
		case Online:
			o += r.credits
		}
	}
	if st.hasCur && st.cur.index == idx {
		e += st.cur.carryIn
	}
	return m, e, o
}

func (st *nState) closeCur(cfg Config, carryOut int, inGrace bool) {
	c := &st.cur
	m, e, o := st.tallyOf(c.index)
	cm, ce, co, tot := nCount(m, e, o, cfg.MandatoryCap, cfg.ElectiveCap)
	c.closed = true
	c.passed = true
	c.passedInGrace = inGrace
	c.fRaw = Tally{Mandatory: m, Elective: e, Online: o}
	c.fCounted = Counted{Mandatory: cm, Elective: ce, Online: co, Total: tot}
	c.fCarryOut = carryOut
	st.closed = append(st.closed, *c)
	st.cur = nCyc{index: c.index + 1, start: c.end, end: c.end + cfg.CycleLengthDays, carryIn: carryOut}
}

func (st *nState) expireCur(cfg Config) {
	c := &st.cur
	m, e, o := st.tallyOf(c.index)
	cm, ce, co, tot := nCount(m, e, o, cfg.MandatoryCap, cfg.ElectiveCap)
	c.closed = true
	c.passed = false
	c.fRaw = Tally{Mandatory: m, Elective: e, Online: o}
	c.fCounted = Counted{Mandatory: cm, Elective: ce, Online: co, Total: tot}
	st.closed = append(st.closed, *c)
	st.hasCur = false
	st.status = CertExpired
}

func (st *nState) advance(now int, cfg Config) {
	for st.status == CertActive {
		c := &st.cur
		if !c.grace {
			if now < c.end {
				return
			}
			m, e, o := st.tallyOf(c.index)
			cm, ce, co, tot := nCount(m, e, o, cfg.MandatoryCap, cfg.ElectiveCap)
			if nPass(cm, tot, cfg) {
				st.closeCur(cfg, nCarry(cm, ce, co, tot, cfg), false)
			} else if cfg.GraceDays > 0 {
				c.grace = true
				c.graceEntered = true
			} else {
				st.expireCur(cfg)
			}
		} else {
			if now < c.end+cfg.GraceDays {
				return
			}
			m, e, o := st.tallyOf(c.index)
			cm, _, _, tot := nCount(m, e, o, cfg.MandatoryCap, cfg.ElectiveCap)
			if nPass(cm, tot, cfg) {
				st.closeCur(cfg, 0, true)
			} else {
				st.expireCur(cfg)
			}
		}
	}
}

func (st *nState) graceRefresh(cfg Config) {
	if st.status != CertActive || !st.hasCur || !st.cur.grace {
		return
	}
	m, e, o := st.tallyOf(st.cur.index)
	cm, _, _, tot := nCount(m, e, o, cfg.MandatoryCap, cfg.ElectiveCap)
	if nPass(cm, tot, cfg) {
		st.closeCur(cfg, 0, true)
	}
}

func (st *nState) attribute(earned, now int, cfg Config) int {
	if !st.hasCur {
		return -1
	}
	c := &st.cur
	if c.grace {
		if earned >= c.start && earned < c.end+cfg.GraceDays {
			return c.index
		}
		return -1
	}
	if earned < c.start || earned >= c.end {
		return -1
	}
	if len(st.closed) > 0 {
		p := st.closed[len(st.closed)-1]
		if p.graceEntered && earned >= p.end && earned < p.end+cfg.GraceDays && now >= p.end+cfg.GraceDays {
			return -1
		}
	}
	return c.index
}

func (st *nState) view(holderID string, gen, asOf int, cfg Config) View {
	v := View{HolderID: holderID, Generation: gen, AsOf: asOf, Cert: st.status}
	mk := func(c nCyc) CycleView {
		cv := CycleView{
			Index:         c.index,
			Start:         c.start,
			End:           c.end,
			Passed:        c.passed,
			PassedInGrace: c.passedInGrace,
			GraceEntered:  c.graceEntered,
			CarryIn:       c.carryIn,
			GraceStart:    c.end,
			GraceEnd:      c.end + cfg.GraceDays,
		}
		var counted Counted
		if c.closed {
			cv.Phase = PhaseClosed
			cv.Raw = c.fRaw
			counted = c.fCounted
			cv.CarryOut = c.fCarryOut
		} else {
			if c.grace {
				cv.Phase = PhaseGrace
			} else {
				cv.Phase = PhaseOpen
			}
			m, e, o := st.tallyOf(c.index)
			cv.Raw = Tally{Mandatory: m, Elective: e, Online: o}
			cm, ce, co, tot := nCount(m, e, o, cfg.MandatoryCap, cfg.ElectiveCap)
			counted = Counted{Mandatory: cm, Elective: ce, Online: co, Total: tot}
			cv.CarryOut = nCarry(cm, ce, co, tot, cfg)
		}
		cv.Counted = counted
		cv.MeetsTotal = counted.Total >= cfg.TotalRequired
		cv.MeetsMandatory = counted.Mandatory >= cfg.MandatoryMin
		cv.Pass = cv.MeetsTotal && cv.MeetsMandatory
		return cv
	}
	for _, c := range st.closed {
		v.Cycles = append(v.Cycles, mk(c))
	}
	if st.hasCur {
		v.Cycles = append(v.Cycles, mk(st.cur))
	}
	return v
}

type nLife struct {
	gen       int
	issueDate int
	regNow    int
	events    []nEvent
}

type nHolder struct {
	lives []*nLife
}

type naive struct {
	cfg       Config
	lastNow   int
	hasLast   bool
	nextRecID int
	holders   map[string]*nHolder
}

func newNaive(cfg Config) *naive {
	return &naive{cfg: cfg, holders: map[string]*nHolder{}}
}

func (h *nHolder) curLife() *nLife { return h.lives[len(h.lives)-1] }

// replay 重放当前一代的全部事件并推进到 now。
func (n *naive) replay(l *nLife, now int) *nState {
	st := &nState{
		status:  CertActive,
		hasCur:  true,
		records: map[string]*nRec{},
	}
	st.cur = nCyc{index: 0, start: l.issueDate, end: l.issueDate + n.cfg.CycleLengthDays}
	for _, e := range l.events {
		if e.now > now {
			break // 历史查询只重放 asOf 之前的事件
		}
		st.advance(e.now, n.cfg)
		switch e.op {
		case "reg":
			r := &nRec{cat: e.cat, credits: e.credits, earned: e.earned, org: e.org, regNow: e.now}
			r.cycle = st.attribute(e.earned, e.now, n.cfg)
			st.records[e.recID] = r
			st.graceRefresh(n.cfg)
		case "correct":
			st.records[e.recID].credits = e.newCredits
			st.graceRefresh(n.cfg)
		case "revoke":
			st.records[e.recID].revoked = true
			st.graceRefresh(n.cfg)
		}
	}
	st.advance(now, n.cfg)
	return st
}

func (n *naive) clockOK(now int) bool { return !n.hasLast || now >= n.lastNow }

func (n *naive) commit(now int) { n.lastNow, n.hasLast = now, true }

func (n *naive) registerHolder(id string, issueDate, now int) ErrKind {
	if id == "" || issueDate < 0 || now < 0 {
		return ErrInvalidParam
	}
	if !n.clockOK(now) {
		return ErrClockRollback
	}
	if h, ok := n.holders[id]; ok {
		st := n.replay(h.curLife(), now)
		if st.status == CertActive {
			return ErrStateNotAllowed
		}
		h.lives = append(h.lives, &nLife{gen: h.curLife().gen + 1, issueDate: issueDate, regNow: now})
	} else {
		n.holders[id] = &nHolder{lives: []*nLife{{gen: 0, issueDate: issueDate, regNow: now}}}
	}
	n.commit(now)
	return ErrNone
}

func (n *naive) registerCredit(holderID string, cat Category, credits, earned int, org string, now int) (string, ErrKind) {
	if holderID == "" || org == "" || credits <= 0 || earned < 0 || now < 0 ||
		(cat != Mandatory && cat != Elective && cat != Online) {
		return "", ErrInvalidParam
	}
	if earned > now {
		return "", ErrInvalidParam
	}
	if !n.clockOK(now) {
		return "", ErrClockRollback
	}
	h, ok := n.holders[holderID]
	if !ok {
		return "", ErrNotFound
	}
	st := n.replay(h.curLife(), now)
	for _, r := range st.records {
		if !r.revoked && r.org == org && r.earned == earned && r.cat == cat {
			return "", ErrDuplicate
		}
	}
	if st.status == CertExpired {
		return "", ErrCertificateExpired
	}
	recID := fmt.Sprintf("R%08d", n.nextRecID)
	n.nextRecID++
	l := h.curLife()
	l.events = append(l.events, nEvent{op: "reg", now: now, recID: recID, cat: cat, credits: credits, earned: earned, org: org})
	n.commit(now)
	return recID, ErrNone
}

func (n *naive) correct(holderID, recID, org string, newCredits, now int) ErrKind {
	if holderID == "" || recID == "" || org == "" || newCredits <= 0 || now < 0 {
		return ErrInvalidParam
	}
	if !n.clockOK(now) {
		return ErrClockRollback
	}
	h, ok := n.holders[holderID]
	if !ok {
		return ErrNotFound
	}
	st := n.replay(h.curLife(), now)
	r, ok := st.records[recID]
	if !ok {
		return ErrNotFound
	}
	if r.revoked || r.org != org {
		return ErrStateNotAllowed
	}
	if now-r.regNow > n.cfg.CorrectionWindowDays {
		return ErrCorrectionWindowExpired
	}
	if st.status == CertExpired {
		return ErrCertificateExpired
	}
	l := h.curLife()
	l.events = append(l.events, nEvent{op: "correct", now: now, recID: recID, newCredits: newCredits})
	n.commit(now)
	return ErrNone
}

func (n *naive) revoke(holderID, recID, org string, now int) ErrKind {
	if holderID == "" || recID == "" || org == "" || now < 0 {
		return ErrInvalidParam
	}
	if !n.clockOK(now) {
		return ErrClockRollback
	}
	h, ok := n.holders[holderID]
	if !ok {
		return ErrNotFound
	}
	st := n.replay(h.curLife(), now)
	r, ok := st.records[recID]
	if !ok {
		return ErrNotFound
	}
	if r.revoked || r.org != org {
		return ErrStateNotAllowed
	}
	if now-r.regNow > n.cfg.CorrectionWindowDays {
		return ErrCorrectionWindowExpired
	}
	if st.status == CertExpired {
		return ErrCertificateExpired
	}
	l := h.curLife()
	l.events = append(l.events, nEvent{op: "revoke", now: now, recID: recID})
	n.commit(now)
	return ErrNone
}

// nView 查询 asOf 时刻视图：选择 regNow <= asOf 的最后一代重放。
func (n *naive) nView(holderID string, asOf int) (View, ErrKind) {
	if asOf < 0 || (n.hasLast && asOf > n.lastNow) {
		return View{}, ErrInvalidParam
	}
	h, ok := n.holders[holderID]
	if !ok {
		return View{}, ErrNotFound
	}
	var life *nLife
	for _, l := range h.lives {
		if l.regNow <= asOf {
			life = l
		} else {
			break
		}
	}
	if life == nil {
		return View{}, ErrNotFound
	}
	st := n.replay(life, asOf)
	return st.view(holderID, life.gen, asOf, n.cfg), ErrNone
}
