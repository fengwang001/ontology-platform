package export

import (
	"context"
	"sync"
)

// Manager runs independent export links concurrently against one data source.
// Each link has its own checkpoint, its own history epochs, and its own active
// cycle lock; work on link A never advances or blocks link B. Within a link,
// every Begin validates against the last confirmed end, so the observable
// result equals that link running serially on its own.
type Manager struct {
	cp      CheckpointStore
	history *MemHistory

	linksMu sync.Mutex
	links   map[string]*linkState
}

type linkState struct {
	mu     sync.Mutex
	active bool
}

// NewManager creates a Manager with the given stores.
func NewManager(cp CheckpointStore, h *MemHistory) *Manager {
	return &Manager{cp: cp, history: h, links: map[string]*linkState{}}
}

func (m *Manager) state(link string) *linkState {
	m.linksMu.Lock()
	defer m.linksMu.Unlock()
	st := m.links[link]
	if st == nil {
		st = &linkState{}
		m.links[link] = st
	}
	return st
}

// ResolvedStart reports the start a new cycle on link must use, applying the
// fixed error-priority decision:
//
//  1. a READABLE checkpoint that disagrees with the declared start is
//     StartMismatch — an interval lie is detected before any I/O;
//  2. an unreadable checkpoint triggers re-derivation from history;
//  3. a hole found while deriving is HistoryGap with the safe prefix;
//  4. resource exhaustion is only possible once output starts.
func (m *Manager) ResolvedStart(link string, declared Position) (Position, error) {
	confirmed, err := m.cp.Load(link)
	if err != nil {
		safe, derr := SafeStart(link, m.history)
		if derr != nil {
			return safe, derr
		}
		if declared != safe {
			return safe, mkErr(KindStartMismatch,
				"link %s: checkpoint unreadable; declared start %d != re-derived safe start %d", link, declared, safe)
		}
		return safe, nil
	}
	if declared != confirmed {
		return confirmed, mkErr(KindStartMismatch,
			"link %s: declared start %d != last confirmed end %d", link, declared, confirmed)
	}
	return confirmed, nil
}

// Begin starts a cycle on a named link. Only one unconfirmed cycle may be
// open per link at a time; different links proceed independently.
func (m *Manager) Begin(_ context.Context, link string, r Range) (*Cycle, error) {
	if err := r.valid(); err != nil {
		return nil, err
	}
	if _, err := m.ResolvedStart(link, r.Start); err != nil {
		return nil, err
	}
	st := m.state(link)
	st.mu.Lock()
	if st.active {
		st.mu.Unlock()
		return nil, mkErr(KindResourceExhausted, "link %s: a cycle is already active; Abandon or Confirm it first", link)
	}
	st.active = true
	st.mu.Unlock()

	epoch := m.history.NextEpoch(link)
	return &Cycle{
		mgr:   m,
		link:  link,
		r:     r,
		epoch: epoch,
		dedup: NewDeduper(),
		open:  true,
	}, nil
}

func (m *Manager) finishCycle(link string) {
	st := m.state(link)
	st.mu.Lock()
	st.active = false
	st.mu.Unlock()
}
