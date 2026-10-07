package derived

import "sort"

func sortStrings(x []string) { sort.Strings(x) }

// Entry returns the current derived entry for one instance, computed against
// the committed snapshot. A concurrent mutator cannot interleave with this
// read; the result is equivalent to a read at some single linearization point.
func (s *Store) Entry(declName, objID string) (Entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.cur.declarations[declName]
	if !ok || s.cur.objects[objID] == nil {
		return Entry{}, false
	}
	return computeEntry(s.cur, d, objID), true
}

// Snapshot is a stable read view. Queries on one snapshot are mutually
// consistent and never observe a torn intermediate state.
type Snapshot struct{ st *state }

// Snapshot pins the currently committed state.
func (s *Store) Snapshot() *Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return &Snapshot{st: s.cur}
}

// Entry computes one entry on the pinned snapshot.
func (sn *Snapshot) Entry(declName, objID string) (Entry, bool) {
	d, ok := sn.st.declarations[declName]
	if !ok || sn.st.objects[objID] == nil {
		return Entry{}, false
	}
	return computeEntry(sn.st, d, objID), true
}

// Lookup returns the sorted distinct object IDs whose entry for declName is
// indexed with key, as of the pinned snapshot.
func (sn *Snapshot) Lookup(declName string, key Value) []string {
	d, ok := sn.st.declarations[declName]
	if !ok {
		return nil
	}
	var out []string
	for id, obj := range sn.st.objects {
		if obj.Type != d.DownstreamType {
			continue
		}
		e := computeEntry(sn.st, d, id)
		if e.State == StateIndexed {
			for _, k := range e.Keys {
				if k == key {
					out = append(out, id)
					break
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// Lookup is the one-shot snapshot lookup on the store.
func (s *Store) Lookup(declName string, key Value) []string {
	return s.Snapshot().Lookup(declName, key)
}

// AllEntries recomputes every entry on the committed snapshot.
func (sn *Snapshot) AllEntries() []Entry {
	var out []Entry
	for _, d := range sn.st.declarations {
		for id, obj := range sn.st.objects {
			if obj.Type == d.DownstreamType {
				out = append(out, computeEntry(sn.st, d, id))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Declaration != out[j].Declaration {
			return out[i].Declaration < out[j].Declaration
		}
		return out[i].ObjectID < out[j].ObjectID
	})
	return out
}
