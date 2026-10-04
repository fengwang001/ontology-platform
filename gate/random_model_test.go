package gate_test

import (
	"testing"

	"ontology/gate"
)

func (m *naiveModel) apply(in op) error {
	if in.now < 0 || in.now > 1_000_000_000_000 {
		return gate.ErrInvalidArgument
	}
	invalid := false
	switch in.kind {
	case "register":
		invalid = in.acct == "" || in.group == ""
	case "limit":
		invalid = in.sym == "" || in.la < 0 || in.lg < 0 || in.day < 0 || in.la > 1e9 || in.lg > 1e9 || in.day > 1e9
	case "hedge":
		invalid = in.acct == "" || in.sym == "" || (in.side != gate.Long && in.side != gate.Short) || in.hedge < 0 || in.hedge > 1e9
	case "order":
		invalid = in.oid == "" || in.acct == "" || in.sym == "" || in.qty < 1 || in.qty > 1e9 ||
			(in.side != gate.Long && in.side != gate.Short) || (in.offset != gate.Open && in.offset != gate.Close)
	case "fill":
		invalid = in.oid == "" || in.qty < 1 || in.qty > 1e9
	case "cancel":
		invalid = in.oid == ""
	}
	if invalid {
		return gate.ErrInvalidArgument
	}
	if in.now < m.now {
		return gate.ErrClockRollback
	}

	var err error
	switch in.kind {
	case "register":
		_, duplicate := m.groups[in.acct]
		if duplicate {
			m.now = in.now
			return gate.ErrDuplicateAccount
		}
		m.groups[in.acct] = in.group
		m.ensure(in.acct, in.sym)
	case "limit":
		m.limits[in.sym] = [3]int64{in.la, in.lg, in.day}
	case "hedge":
		if _, ok := m.groups[in.acct]; !ok {
			return gate.ErrNotFound
		}
		m.hedges[hedgeKey(in.acct, in.sym, in.side)] = in.hedge
	case "order":
		err = m.applyOrder(in)
	case "fill":
		err = m.applyFill(in)
	case "cancel":
		err = m.applyCancel(in)
	case "reset":
		for acct := range m.filled {
			m.filled[acct] = map[string]int64{}
		}
	}
	if err != nil {
		return err
	}
	m.now = in.now
	return nil
}

func (m *naiveModel) applyOrder(in op) error {
	if _, ok := m.groups[in.acct]; !ok {
		return gate.ErrNotFound
	}
	limits, ok := m.limits[in.sym]
	if !ok {
		return gate.ErrNotFound
	}
	if _, ok := m.orders[in.oid]; ok {
		return gate.ErrDuplicateOrder
	}
	m.ensure(in.acct, in.sym)
	if in.offset == gate.Close {
		if in.qty > m.closeAvailable(in.acct, in.sym, in.side) {
			return gate.ErrCloseOverLimit
		}
	} else {
		exposureValue := m.exposure(in.acct, in.sym, in.side)
		hedge := m.hedges[hedgeKey(in.acct, in.sym, in.side)]
		if exposureValue+in.qty > limits[0]+hedge {
			return gate.ErrAccountLimit
		}
		oldContribution := max64(0, exposureValue-hedge)
		newContribution := max64(0, exposureValue+in.qty-hedge)
		if m.groupExposure(m.groups[in.acct], in.sym, in.side)-oldContribution+newContribution > limits[1] {
			return gate.ErrGroupLimit
		}
		if m.dayOpen(in.acct, in.sym)+in.qty > limits[2] {
			return gate.ErrDayOpenLimit
		}
	}
	state := m.state(in.acct, in.sym, in.side)
	if in.offset == gate.Open {
		state.pendingOpen += in.qty
	} else {
		state.pendingClose += in.qty
	}
	m.setSide(in.acct, in.sym, in.side, state)
	m.orders[in.oid] = modelOrder{acct: in.acct, sym: in.sym, side: in.side, offset: in.offset, remain: in.qty}
	return nil
}

func (m *naiveModel) applyFill(in op) error {
	entry, ok := m.orders[in.oid]
	if !ok {
		return gate.ErrNotFound
	}
	if entry.finished || entry.remain == 0 || in.qty > entry.remain {
		return gate.ErrInvalidState
	}
	state := m.state(entry.acct, entry.sym, entry.side)
	if entry.offset == gate.Open {
		state.pendingOpen -= in.qty
		state.position += in.qty
		m.filled[entry.acct][entry.sym] += in.qty
	} else {
		state.position -= in.qty
		state.pendingClose -= in.qty
	}
	m.setSide(entry.acct, entry.sym, entry.side, state)
	entry.remain -= in.qty
	entry.finished = entry.remain == 0
	m.orders[in.oid] = entry
	return nil
}

func (m *naiveModel) applyCancel(in op) error {
	entry, ok := m.orders[in.oid]
	if !ok {
		return gate.ErrNotFound
	}
	if entry.finished || entry.remain == 0 {
		return gate.ErrInvalidState
	}
	state := m.state(entry.acct, entry.sym, entry.side)
	if entry.offset == gate.Open {
		state.pendingOpen -= entry.remain
	} else {
		state.pendingClose -= entry.remain
	}
	m.setSide(entry.acct, entry.sym, entry.side, state)
	entry.remain = 0
	entry.finished = true
	m.orders[in.oid] = entry
	return nil
}

func (m *naiveModel) verify(t *testing.T, g *gate.Gateway) {
	t.Helper()
	for acct, group := range m.groups {
		for _, side := range []gate.Side{gate.Long, gate.Short} {
			checkValue(t, "position", g.Position(b(acct), b("S"), side), m.position(acct, "S", side))
			checkValue(t, "pending open", g.PendingOpen(b(acct), b("S"), side), m.pendingOpen(acct, "S", side))
			checkValue(t, "pending close", g.PendingClose(b(acct), b("S"), side), m.pendingClose(acct, "S", side))
			checkValue(t, "exposure", g.Exposure(b(acct), b("S"), side), m.exposure(acct, "S", side))
			checkValue(t, "group exposure", g.GroupExposure(b(group), b("S"), side), m.groupExposure(group, "S", side))
		}
		checkValue(t, "day open", g.DayOpen(b(acct), b("S")), m.dayOpen(acct, "S"))
	}
}

func checkValue(t *testing.T, name string, got, want int64) {
	t.Helper()
	if got != want {
		t.Fatalf("%s=%d want %d", name, got, want)
	}
}
