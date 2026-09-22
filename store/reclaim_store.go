package store

import "ontology/txid"

// Reclaim advances the watermark and incrementally prunes candidate keys.
// maxKeys <= 0 drains the whole current queue in one pass. It returns the
// number of chain versions actually examined.
func (s *Store) Reclaim(maxKeys int) int {
	rep := s.rec.Advance(func(key string, wm txid.ID) int {
		s.mu.Lock()
		defer s.mu.Unlock()
		ch := s.chains[key]
		if ch == nil {
			return 0
		}
		r := ch.PruneBelow(wm)
		s.total -= r.Removed
		// Still a pending barrier or more shadowed versions may remain; keep
		// the key queued so a later (higher) watermark can finish the job
		// without ever scanning unrelated keys.
		if ch.Len() > 1 && ch.HasCommittedBelow(wm) {
			s.rec.Notify(key)
		}
		return r.Examined
	}, maxKeys)
	return rep.Examined
}

// ExaminedLast returns versions examined by the most recent Reclaim.
func (s *Store) ExaminedLast() int { return s.rec.ExaminedLast() }

// Stats is a point-in-time, read-only view of store counters.
type Stats struct {
	VisibleVersionsForKey int
	ActiveSnapshots       int
	Watermark             txid.ID
	TotalVersions         int
}

// StatsFor returns read-only counters from v's point of view. Querying never
// advances the watermark or any other state; two calls at the same instant
// return identical values.
func (s *Store) StatsFor(v *View, key string) Stats {
	s.mu.Lock()
	vis := 0
	if ch := s.chains[key]; ch != nil {
		vis = ch.VisibleCount(v.snap)
	}
	total := s.total
	s.mu.Unlock()
	return Stats{
		VisibleVersionsForKey: vis,
		ActiveSnapshots:       s.reg.Count(),
		Watermark:             s.rec.Watermark(),
		TotalVersions:         total,
	}
}
