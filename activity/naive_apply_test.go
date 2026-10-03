package activity

import "errors"

func (m *reference) apply(o op) (error, int, int64) {
	if o.t < m.clock {
		return ErrClock, 0, 0
	}
	m.settle(o.t)
	reject := func(err error) (error, int, int64) { return err, 0, 0 }
	switch o.kind {
	case opStart:
		if m.state == StateTerminal {
			return reject(ErrTerminal)
		}
		if m.state != Scheduled {
			return reject(ErrState)
		}
		m.state, m.r, m.h = Running, o.t, o.t
		m.clock = o.t
		return nil, m.k, m.progress
	case opHB:
		if m.k != o.k {
			return reject(ErrStale)
		}
		if m.state == StateTerminal {
			return reject(ErrTerminal)
		}
		if m.state != Running {
			return reject(ErrState)
		}
		m.h, m.progress = o.t, o.prog
		m.clock = o.t
	case opComplete:
		if m.k != o.k {
			return reject(ErrStale)
		}
		if m.state == StateTerminal {
			return reject(ErrTerminal)
		}
		if m.state != Running {
			return reject(ErrState)
		}
		m.term, m.reason, m.termAt = TermCompleted, None, o.t
		m.state, m.clock = StateTerminal, o.t
	case opFailRetry, opFailNoRetry:
		if m.k != o.k {
			return reject(ErrStale)
		}
		if m.state == StateTerminal {
			return reject(ErrTerminal)
		}
		if m.state != Running {
			return reject(ErrState)
		}
		if o.kind == opFailNoRetry {
			m.term, m.reason, m.termAt = TermFailed, App, o.t
			m.state, m.clock = StateTerminal, o.t
			return nil, 0, 0
		}
		m.clock = o.t
		m.failApp(o.t)
	}
	return nil, 0, 0
}

func (m *reference) failApp(at int64) {
	if m.k >= m.cfg.M {
		m.term, m.reason, m.termAt, m.state = TermFailed, App, at, StateTerminal
		return
	}
	b := backoffRef(m.cfg, m.k)
	gp := at + b
	if sc, ok := m.scAt(); ok && gp >= sc {
		m.term, m.reason, m.termAt, m.state = TermFailed, App, at, StateTerminal
		return
	}
	m.k++
	m.state, m.waitEnd = Waiting, gp
}

func (m *reference) status(at int64) Status {
	cp := *m
	cp.settle(at)
	return Status{State: cp.state, Attempt: cp.k, Terminal: cp.term, Reason: cp.reason, At: cp.termAt}
}

func errEq(a, b error) bool {
	return errors.Is(a, b) || (a == nil && b == nil)
}
