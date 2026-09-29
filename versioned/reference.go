package versioned

import "sync"

// Reference is a naive LWW store whose tombstones are never collected.
type Reference struct {
	mu sync.Mutex

	live  map[string]Row
	tombs map[string]int64
}

// NewReference creates a Reference.
func NewReference() *Reference {
	return &Reference{
		live:  make(map[string]Row),
		tombs: make(map[string]int64),
	}
}

// Commit applies the same LWW rule as Store but keeps tombstones forever and
// tracks no watermark. It assumes events are already valid; comparison tests
// feed both implementations identical, pre-validated batches.
func (r *Reference) Commit(events []*Event) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, e := range events {
		current := int64(0)
		if row, ok := r.live[e.Key]; ok {
			current = row.Version
		} else if tv, ok := r.tombs[e.Key]; ok {
			current = tv
		}
		if e.Version <= current {
			continue
		}
		switch e.Op {
		case OpWrite:
			r.live[e.Key] = Row{Version: e.Version, Value: cloneBytes(e.Value)}
			delete(r.tombs, e.Key)
		case OpDelete:
			delete(r.live, e.Key)
			r.tombs[e.Key] = e.Version
		}
	}
}

// Get returns the live row for key.
func (r *Reference) Get(key string) (Row, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	row, ok := r.live[key]
	if ok {
		row.Value = cloneBytes(row.Value)
	}
	return row, ok
}

// TombstoneVersion reports whether a tombstone exists for key.
func (r *Reference) TombstoneVersion(key string) (int64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	tv, ok := r.tombs[key]
	return tv, ok
}

// LiveSnapshot returns a deep copy of the live rows.
func (r *Reference) LiveSnapshot() map[string]Row {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]Row, len(r.live))
	for key, row := range r.live {
		out[key] = Row{Version: row.Version, Value: cloneBytes(row.Value)}
	}
	return out
}
