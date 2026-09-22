package store

import (
	"ontology/txid"
	"ontology/version"
)

// crashLocked marks the volatile store unusable. The durable journal is kept
// so Recover can rebuild. Caller holds s.mu.
func (s *Store) crashLocked() {
	s.dead = true
}

// Journal returns the durable journal so tests can carry it across a
// simulated crash to Recover without touching a real filesystem.
func (s *Store) Journal() *journal {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.journal
}

// Dead reports whether the store crashed and awaits recovery.
func (s *Store) Dead() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dead
}

// Recover rebuilds volatile state solely from a durable journal. The rule is
// all-or-nothing:
//   - a record with Committed=true replays (fully visible);
//   - every other record is discarded as if it never existed (no pending
//     node, no dangling index slot, no half-visible version).
//
// nextID should resume after the highest begun id; pass txid.Invalid to use
// journal.MaxID()+1.
func Recover(j *journal, cfg Config, injected txid.Source, nextID txid.ID) *Store {
	if nextID == txid.Invalid {
		nextID = j.MaxID() + 1
		if nextID == txid.Invalid {
			nextID = 1
		}
	}
	var src txid.Source = injected
	if src == nil {
		src = txid.NewSourceAt(nextID)
	}
	s := New(cfg, WithSource(src))
	s.journal = newJournal()

	// Rebuild only committed transactions, in begin order.
	for _, id := range j.order {
		rec := j.recs[id]
		if rec == nil {
			continue
		}
		if !rec.Committed {
			continue
		}
		s.journal.Begin(id)
		keys := map[string]struct{}{}
		for _, w := range rec.Writes {
			s.journal.AppendWrite(id, w)
			kind := version.KindValue
			if w.Op == opDelete {
				kind = version.KindDelete
			}
			ch := s.chains[w.Key]
			if ch == nil {
				ch = &version.Chain{}
				s.chains[w.Key] = ch
			}
			ch.Append(id, kind, w.Value, 0)
			s.total++
			keys[w.Key] = struct{}{}
		}
		for k := range keys {
			s.chains[k].Commit(id, id)
		}
		s.journal.Commit(id)
	}
	// Requeue every key that can hold shadowed versions; reclamation resumes
	// incrementally from a clean watermark.
	for key, ch := range s.chains {
		if ch.CommittedCount() >= 2 {
			s.rec.Notify(key)
		}
	}
	return s
}
