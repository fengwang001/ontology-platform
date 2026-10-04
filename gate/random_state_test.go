package gate_test

import (
	"fmt"

	"ontology/gate"
)

func (m *naiveModel) ensure(acct, sym string) {
	if m.sides[acct] == nil {
		m.sides[acct] = map[string][2]modelSide{}
		m.filled[acct] = map[string]int64{}
	}
	if sym != "" {
		m.sides[acct][sym] = m.sides[acct][sym]
	}
}

func (m *naiveModel) state(acct, sym string, side gate.Side) modelSide {
	m.ensure(acct, sym)
	return m.sides[acct][sym][sideIndex(side)]
}

func (m *naiveModel) setSide(acct, sym string, side gate.Side, value modelSide) {
	m.ensure(acct, sym)
	values := m.sides[acct][sym]
	values[sideIndex(side)] = value
	m.sides[acct][sym] = values
}

func (m *naiveModel) position(acct, sym string, side gate.Side) int64 {
	return m.state(acct, sym, side).position
}

func (m *naiveModel) pendingOpen(acct, sym string, side gate.Side) int64 {
	return m.state(acct, sym, side).pendingOpen
}

func (m *naiveModel) pendingClose(acct, sym string, side gate.Side) int64 {
	return m.state(acct, sym, side).pendingClose
}

func (m *naiveModel) exposure(acct, sym string, side gate.Side) int64 {
	state := m.state(acct, sym, side)
	return state.position + state.pendingOpen
}

func (m *naiveModel) closeAvailable(acct, sym string, side gate.Side) int64 {
	state := m.state(acct, sym, side)
	return state.position - state.pendingClose
}

func (m *naiveModel) dayOpen(acct, sym string) int64 {
	values := m.sides[acct][sym]
	return m.filled[acct][sym] + values[0].pendingOpen + values[1].pendingOpen
}

func (m *naiveModel) groupExposure(group, sym string, side gate.Side) int64 {
	var total int64
	for acct, acctGroup := range m.groups {
		if acctGroup != group {
			continue
		}
		hedge := m.hedges[hedgeKey(acct, sym, side)]
		contribution := m.exposure(acct, sym, side) - hedge
		total += max64(0, contribution)
	}
	return total
}

func hedgeKey(acct, sym string, side gate.Side) string {
	return fmt.Sprintf("%s|%s|%d", acct, sym, side)
}

func sideIndex(side gate.Side) int {
	if side == gate.Short {
		return 1
	}
	return 0
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
