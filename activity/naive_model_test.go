package activity

import "ontology/deadline"

type opKind uint8

const (
	opStart opKind = iota
	opHB
	opComplete
	opFailRetry
	opFailNoRetry
)

type op struct {
	kind opKind
	t    int64
	k    int
	prog int64
}

// reference 是逐毫秒朴素模型：先 settle 到期/唤醒（SC>S2C>HB>S2S），再受理操作。
type reference struct {
	cfg                            Config
	t0, g, r, h, waitEnd, progress int64
	k                              int
	state                          State
	term                           Terminal
	reason                         Reason
	termAt                         int64
	clock                          int64
}

func (m *reference) scAt() (int64, bool) {
	if m.cfg.SC == 0 {
		return 0, false
	}
	return m.t0 + m.cfg.SC, true
}

func (m *reference) settle(now int64) {
	for m.state != StateTerminal {
		if m.state == Waiting {
			if sc, ok := m.scAt(); ok && now >= sc {
				m.term, m.reason = TermTimedOutSC, SCReason
				m.termAt, m.state = sc, StateTerminal
				return
			}
			if now < m.waitEnd {
				return
			}
			m.g = m.waitEnd
			m.state = Scheduled // 唤醒后进入下一轮，按新状态重建到期项
			continue
		}
		type cand struct {
			at int64
			k  deadline.Kind
		}
		var cs []cand
		if at, ok := m.scAt(); ok {
			cs = append(cs, cand{at, deadline.SC})
		}
		if m.state == Scheduled && m.cfg.S2S != 0 {
			cs = append(cs, cand{m.g + m.cfg.S2S, deadline.S2S})
		}
		if m.state == Running {
			if m.cfg.S2C != 0 {
				cs = append(cs, cand{m.r + m.cfg.S2C, deadline.S2C})
			}
			if m.cfg.HB != 0 {
				cs = append(cs, cand{m.h + m.cfg.HB, deadline.HB})
			}
		}
		pick := -1
		for i, c := range cs {
			if c.at > now {
				continue
			}
			if pick < 0 || c.at < cs[pick].at ||
				(c.at == cs[pick].at && c.k < cs[pick].k) {
				pick = i
			}
		}
		if pick < 0 {
			return
		}
		switch cs[pick].k {
		case deadline.SC:
			m.term, m.reason = TermTimedOutSC, SCReason
			m.termAt, m.state = cs[pick].at, StateTerminal
		case deadline.S2S:
			m.term, m.reason = TermTimedOutS2S, S2S
			m.termAt, m.state = cs[pick].at, StateTerminal
		case deadline.S2C:
			m.fail(deadline.S2C, cs[pick].at)
		case deadline.HB:
			m.fail(deadline.HB, cs[pick].at)
		}
	}
}

func (m *reference) fail(kind deadline.Kind, at int64) {
	rho := S2C
	if kind == deadline.HB {
		rho = HB
	}
	if m.k >= m.cfg.M {
		m.term, m.reason, m.termAt, m.state = TermFailed, rho, at, StateTerminal
		return
	}
	b := backoffRef(m.cfg, m.k)
	gp := at + b
	if sc, ok := m.scAt(); ok && gp >= sc {
		m.term, m.reason, m.termAt, m.state = TermFailed, rho, at, StateTerminal
		return
	}
	m.k++
	m.state, m.waitEnd = Waiting, gp
}

func backoffRef(c Config, k int) int64 {
	d := c.D0
	for i := 1; i < k; i++ {
		d *= 2
		if d > c.Cap {
			return c.Cap
		}
	}
	if d > c.Cap {
		return c.Cap
	}
	return d
}
