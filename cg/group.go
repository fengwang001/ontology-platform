package cg

// snapshot holds the in-progress snapshot state for a group.
type snapshot struct {
	id        uint64
	phase     Phase
	deadline  int64
	point     int64
	confirmed map[string]bool
	// pending is the number of volumes that have not confirmed. Checking
	// whether a confirmation is the last one is therefore O(1): it is the
	// last confirmation iff pending becomes zero.
	pending int
	// cutoffs is fixed in a single O(m) pass only at the instant the last
	// confirmation closes the freeze window.
	cutoffs map[string]uint64
}

// group is a consistency group of 2..16 volumes.
type group struct {
	id       string
	members  []*volume
	index    map[string]*volume
	snap     *snapshot
	lastSnap *SnapshotRecord
	maxHold  int64
}

func newGroup(id string, members []*volume, maxHold int64) *group {
	idx := make(map[string]*volume, len(members))
	for _, v := range members {
		idx[v.id] = v
	}
	return &group{id: id, members: members, index: idx, maxHold: maxHold}
}

func (g *group) idle() bool { return g.snap == nil }

func (g *group) has(vid string) bool {
	_, ok := g.index[vid]
	return ok
}

// begin starts a snapshot. Caller guarantees the group is idle.
func (g *group) begin(id uint64, deadline int64) {
	s := &snapshot{
		id:        id,
		phase:     Freezing,
		deadline:  deadline,
		confirmed: make(map[string]bool, len(g.members)),
		pending:   len(g.members),
	}
	g.snap = s
}

// dueAutoAbort reports whether time t has crossed a timeout boundary of
// the in-progress snapshot. Both checks are strict inequalities; an event
// exactly on the boundary is still valid.
func (s *snapshot) dueAutoAbort(t int64, maxHold int64) (bool, string) {
	switch s.phase {
	case Freezing:
		if t > s.deadline {
			return true, "freeze deadline passed without full confirmation"
		}
	case Frozen:
		if t > s.point+maxHold {
			return true, "frozen hold duration exceeded without commit"
		}
	}
	return false, ""
}

// needsQueue reports whether a write to this group's volume at time t must
// be queued. It is O(1): one nil check, one phase check, one map lookup and
// in the frozen case no per-volume work at all.
func (g *group) needsQueue(volID string) bool {
	s := g.snap
	if s == nil {
		return false
	}
	if s.phase == Frozen {
		return true
	}
	return s.confirmed[volID]
}

// confirm marks a volume frozen. It returns true when this confirmation
// closed the snapshot (last confirmation). The last-confirmation decision is
// a single integer decrement and comparison, independent of group size.
// Closing the snapshot costs O(m) exactly once, solely to read each
// volume's current sequence into the immutable cutoff vector.
func (g *group) confirm(volID string, at int64) bool {
	s := g.snap
	s.confirmed[volID] = true
	s.pending--
	if s.pending > 0 {
		return false
	}
	s.phase = Frozen
	s.point = at
	s.cutoffs = make(map[string]uint64, len(g.members))
	for _, v := range g.members {
		s.cutoffs[v.id] = v.seq
	}
	return true
}

// freezeAndDrain is the shared ending used by commit, manual abort and
// auto-abort: drop the snapshot, unfreeze every volume and apply queued
// writes per volume in arrival order. Draining each volume is O(qi) where
// qi is that volume's queued count; total work is linear in queued writes,
// which is unavoidable because each queued write must be applied.
func (g *group) freezeAndDrain() {
	g.snap = nil
	for _, v := range g.members {
		v.drain()
	}
}

// commit finalizes the snapshot and returns its record.
func (g *group) commit() *SnapshotRecord {
	s := g.snap
	rec := &SnapshotRecord{
		ID:       s.id,
		GroupID:  g.id,
		Point:    s.point,
		Deadline: s.deadline,
		Cutoffs:  make(map[string]uint64, len(s.cutoffs)),
	}
	for k, val := range s.cutoffs {
		rec.Cutoffs[k] = val
	}
	g.lastSnap = rec
	g.freezeAndDrain()
	return rec
}

func (g *group) abort() {
	g.freezeAndDrain()
}
