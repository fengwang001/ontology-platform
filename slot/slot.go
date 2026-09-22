// Package slot implements the element set of a single slot in one wheel
// level: insert, remove by handle, and take-all. It depends on nothing.
// A Slot is not goroutine-safe; callers synchronize.
package slot

// Entry is one element stored in a Slot.
type Entry struct {
	Handle uint64
	Value  any
}

// Slot is an insertion-ordered set of entries keyed by handle.
type Slot struct {
	entries []Entry
	index   map[uint64]int
}

// Insert adds v under handle. Duplicate handles are ignored.
func (s *Slot) Insert(handle uint64, v any) {
	if s.index == nil {
		s.index = make(map[uint64]int)
	}
	if _, dup := s.index[handle]; dup {
		return
	}
	s.index[handle] = len(s.entries)
	s.entries = append(s.entries, Entry{Handle: handle, Value: v})
}

// Remove deletes the entry with the given handle, if present.
func (s *Slot) Remove(handle uint64) (any, bool) {
	i, ok := s.index[handle]
	if !ok {
		return nil, false
	}
	v := s.entries[i].Value
	last := len(s.entries) - 1
	s.entries[i] = s.entries[last]
	s.index[s.entries[i].Handle] = i
	s.entries = s.entries[:last]
	delete(s.index, handle)
	return v, true
}

// TakeAll returns all entries and empties the slot.
func (s *Slot) TakeAll() []Entry {
	out := s.entries
	s.entries = nil
	s.index = nil
	return out
}

// Len reports the number of entries.
func (s *Slot) Len() int { return len(s.entries) }

// Entries exposes the current entries for read-only inspection.
func (s *Slot) Entries() []Entry { return s.entries }
