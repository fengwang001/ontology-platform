// Package log holds the in-memory Raft log entries and 1-based index access.
// Index 0 is a sentinel entry with term 0; real entries start at index 1.
package log

// Entry is one log record: only its term is needed for bookkeeping here.
type Entry struct {
	Term int
}

// Log is a slice of entries. The zero value is not ready; use New.
type Log struct {
	entries []Entry
}

// New returns an empty log containing just the index-0 sentinel (term 0).
func New() *Log {
	return &Log{entries: []Entry{{Term: 0}}}
}

// Append appends an entry of the given term and returns its new 1-based index.
func (l *Log) Append(term int) int {
	l.entries = append(l.entries, Entry{Term: term})
	return len(l.entries) - 1
}

// Len returns the number of real entries (excluding the sentinel).
func (l *Log) Len() int {
	return len(l.entries) - 1
}

// Term returns the term of the entry at index i.
// i must satisfy 0 <= i <= Len(); callers guarantee the bound.
func (l *Log) Term(i int) int {
	return l.entries[i].Term
}
