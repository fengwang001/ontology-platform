package roadnet

import "fmt"

// AddEdge registers a directed edge u -> v whose first record is the
// given profile effective from time 0, and returns its edge id.
// Ids start at 1 and increase strictly; rejected calls consume no id.
// Rejection order: ErrInvalidParam, then ErrEdgeLimit.
func (nt *Net) AddEdge(u, v int, profile []Segment) (int, error) {
	if !nt.validNode(u) || !nt.validNode(v) {
		return 0, fmt.Errorf("%w: endpoints (%d,%d) outside [0,%d)", ErrInvalidParam, u, v, nt.n)
	}
	if u == v {
		return 0, fmt.Errorf("%w: self loop on node %d", ErrInvalidParam, u)
	}
	if !validProfile(profile) {
		return 0, fmt.Errorf("%w: malformed profile", ErrInvalidParam)
	}
	nt.mu.Lock()
	defer nt.mu.Unlock()
	if len(nt.edges) >= nt.edgeCap {
		return 0, fmt.Errorf("%w: %d edges already registered", ErrEdgeLimit, nt.edgeCap)
	}
	nt.version++
	id := len(nt.edges) + 1
	e := &edge{
		id:     id,
		u:      u,
		v:      v,
		regVer: nt.version,
		records: []record{{
			eff:     0,
			profile: cloneProfile(profile),
			regVer:  nt.version,
		}},
	}
	nt.edges = append(nt.edges, e)
	nt.adj[u] = append(nt.adj[u], id)
	return id, nil
}

// Announce appends or replaces a record on an edge. eff must be at
// least now and at least the edge's last record's effective time; an
// eff equal to the last record's replaces it (the replaced record is
// kept for historical queries and does not count towards the record
// limit), otherwise the record is appended.
// Rejection order: ErrInvalidParam, ErrEdgeNotFound, ErrRetroactive,
// ErrOutOfOrder, ErrRecordLimit.
func (nt *Net) Announce(edgeID int, eff int64, profile []Segment) error {
	if edgeID < 1 || edgeID > nt.edgeCap {
		return fmt.Errorf("%w: edge id %d outside [1,%d]", ErrInvalidParam, edgeID, nt.edgeCap)
	}
	if !validTime(eff) {
		return fmt.Errorf("%w: effective time %d outside [0,%d]", ErrInvalidParam, eff, MaxTime)
	}
	if !validProfile(profile) {
		return fmt.Errorf("%w: malformed profile", ErrInvalidParam)
	}
	nt.mu.Lock()
	defer nt.mu.Unlock()
	if edgeID > len(nt.edges) {
		return fmt.Errorf("%w: edge id %d never assigned", ErrEdgeNotFound, edgeID)
	}
	e := nt.edges[edgeID-1]
	if eff < nt.now {
		return fmt.Errorf("%w: effective time %d before now=%d", ErrRetroactive, eff, nt.now)
	}
	last := e.lastLive()
	if eff < last.eff {
		return fmt.Errorf("%w: effective time %d before last record %d", ErrOutOfOrder, eff, last.eff)
	}
	if eff > last.eff && e.liveCount() >= MaxRecords {
		return fmt.Errorf("%w: edge %d already has %d records", ErrRecordLimit, edgeID, MaxRecords)
	}
	nt.version++
	if eff == last.eff {
		last.replVer = nt.version
	}
	e.records = append(e.records, record{
		eff:     eff,
		profile: cloneProfile(profile),
		regVer:  nt.version,
	})
	return nil
}

// Advance moves the system clock to t. Rejection order:
// ErrInvalidParam, then ErrClockBack. Advancing does not change the
// network version.
func (nt *Net) Advance(t int64) error {
	if !validTime(t) {
		return fmt.Errorf("%w: time %d outside [0,%d]", ErrInvalidParam, t, MaxTime)
	}
	nt.mu.Lock()
	defer nt.mu.Unlock()
	if t < nt.now {
		return fmt.Errorf("%w: time %d before now=%d", ErrClockBack, t, nt.now)
	}
	nt.now = t
	return nil
}
