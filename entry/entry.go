// Package entry defines a single Raft log entry and the contiguous,
// 1-indexed log primitives. It depends on no other package.
package entry

// Entry is one log record. Index starts at 1 and is contiguous; there are
// never holes in a log.
type Entry struct {
	Term  int
	Index int
	Cmd   string
}

// Log is a contiguous slice of entries with 1-based indexes. It is NOT
// safe for concurrent use; callers (package raft) provide synchronization.
type Log struct {
	es []Entry
}

// NewLog returns an empty log.
func NewLog() *Log { return &Log{} }

// Len returns the number of entries (the index of the last entry, or 0).
func (l *Log) Len() int { return len(l.es) }

// Append adds e to the tail. Callers must keep indexes contiguous.
func (l *Log) Append(e Entry) {
	l.es = append(l.es, e)
}

// At returns the entry at 1-based index i and true, or a zero entry and
// false if i < 1 or i > Len().
func (l *Log) At(i int) (Entry, bool) {
	if i < 1 || i > len(l.es) {
		return Entry{}, false
	}
	return l.es[i-1], true
}

// After returns a defensive copy of entries with indexes greater than i.
func (l *Log) After(i int) []Entry {
	if i < 0 {
		i = 0
	}
	if i > len(l.es) {
		i = len(l.es)
	}
	out := make([]Entry, len(l.es)-i)
	copy(out, l.es[i:])
	return out
}

// Truncate drops every entry whose index is greater than i, keeping the
// prefix 1..i. i == 0 empties the log.
func (l *Log) Truncate(i int) {
	if i < 0 {
		i = 0
	}
	if i > len(l.es) {
		i = len(l.es)
	}
	l.es = l.es[:i]
}

// Clone returns a defensive copy of the whole log.
func (l *Log) Clone() []Entry {
	out := make([]Entry, len(l.es))
	copy(out, l.es)
	return out
}

// PrefixMatch reports the Log Matching Property anchor: the entry at
// prevIndex (if any) must exist and carry prevTerm. prevIndex 0 is the
// empty-prefix anchor and always matches. Term comparison is mandatory:
// matching by index alone would let divergent entries at the same index
// be treated as equal.
func (l *Log) PrefixMatch(prevIndex, prevTerm int) bool {
	if prevIndex == 0 {
		return true
	}
	e, ok := l.At(prevIndex)
	return ok && e.Term == prevTerm
}
