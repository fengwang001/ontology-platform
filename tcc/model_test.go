package tcc

// naive 是不依赖堆与回滚机制的逐步朴素模型：
// 每次操作先线性扫描做虚拟到期，再严格按规格判定。
type nBranch struct {
	st       BranchState
	acct     string
	amt      int64
	deadline int64
	reason   CancelReason
}

type naive struct {
	ttl   int64
	capN  int
	now   int64
	bal   map[string]int64
	fz    map[string]int64
	state map[string]nBranch
}

func newNaive(ttl int64, capN int, bal map[string]int64) *naive {
	b := map[string]int64{}
	for k, v := range bal {
		b[k] = v
	}
	return &naive{ttl: ttl, capN: capN, bal: b, fz: map[string]int64{}, state: map[string]nBranch{}}
}

func (m *naive) virtualExpiry(now int64) map[string]bool {
	ex := map[string]bool{}
	for k, b := range m.state {
		if b.st == StateTried && now >= b.deadline {
			ex[k] = true
		}
	}
	return ex
}

func (m *naive) view(k string, ex map[string]bool) (nBranch, bool) {
	b, ok := m.state[k]
	if ok && ex[k] {
		b.st = StateCancelled
		b.reason = Expired
	}
	return b, ok
}

func (m *naive) avail(acct string, now int64) int64 {
	ex := m.virtualExpiry(now)
	rel := int64(0)
	for k := range ex {
		if m.state[k].acct == acct {
			rel += m.state[k].amt
		}
	}
	return m.bal[acct] - m.fz[acct] + rel
}

func (m *naive) materialize(ex map[string]bool) {
	for k := range ex {
		b := m.state[k]
		m.fz[b.acct] -= b.amt
		m.state[k] = nBranch{st: StateCancelled, acct: b.acct, amt: b.amt, reason: Expired}
	}
}

func (m *naive) try(xid, br, acct string, amt, now int64) error {
	if xid == "" || br == "" || acct == "" || amt < 1 || amt > maxAmount ||
		now < 0 || now > maxNow {
		return ErrInvalid
	}
	if now < m.now {
		return ErrClock
	}
	ex := m.virtualExpiry(now)
	k := key(xid, br)
	b, ok := m.view(k, ex)
	if ok {
		switch b.st {
		case StateTried:
			if b.acct != acct || b.amt != amt {
				return ErrMismatch
			}
		case StateConfirmed:
			return ErrState
		default:
			return ErrHanging
		}
	} else {
		if len(m.state) >= m.capN {
			return ErrCapacity
		}
		if m.avail(acct, now) < amt {
			return ErrInsufficient
		}
		m.fz[acct] += amt
		m.state[k] = nBranch{st: StateTried, acct: acct, amt: amt, deadline: now + m.ttl}
	}
	m.materialize(ex)
	m.now = now
	return nil
}

func (m *naive) confirm(xid, br string, now int64) error {
	if xid == "" || br == "" || now < 0 || now > maxNow {
		return ErrInvalid
	}
	if now < m.now {
		return ErrClock
	}
	ex := m.virtualExpiry(now)
	k := key(xid, br)
	b, ok := m.view(k, ex)
	if !ok {
		return ErrNoBranch
	}
	switch b.st {
	case StateTried:
		m.fz[b.acct] -= b.amt
		m.bal[b.acct] -= b.amt
		m.state[k] = nBranch{st: StateConfirmed, acct: b.acct, amt: b.amt}
	case StateConfirmed:
	default:
		if b.reason == Expired {
			return ErrExpired
		}
		return ErrConflict
	}
	m.materialize(ex)
	m.now = now
	return nil
}

func (m *naive) cancel(xid, br string, now int64) error {
	if xid == "" || br == "" || now < 0 || now > maxNow {
		return ErrInvalid
	}
	if now < m.now {
		return ErrClock
	}
	ex := m.virtualExpiry(now)
	k := key(xid, br)
	b, ok := m.view(k, ex)
	switch {
	case !ok:
		if len(m.state) >= m.capN {
			return ErrCapacity
		}
		m.state[k] = nBranch{st: StateCancelled, reason: Empty}
	case b.st == StateTried:
		m.fz[b.acct] -= b.amt
		m.state[k] = nBranch{st: StateCancelled, acct: b.acct, amt: b.amt, reason: Cancel}
	case b.st == StateCancelled:
	default:
		return ErrConflict
	}
	m.materialize(ex)
	m.now = now
	return nil
}
