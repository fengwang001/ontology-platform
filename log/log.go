// Package log is the global append-only write log.
// LSNs start at 1 and increase by one per append. Entries are addressed
// directly by slice index, so an (after, through] range fetch is O(1) to
// locate plus O(through-after) to hand back — it never scans from LSN 1.
package log

// Entry is one write: key/val at a globally ordered lsn.
type Entry struct {
	Key string
	Val string
	LSN int
}

// Log is an append-only sequence of writes. It is not safe for concurrent
// use on its own; the api package serializes all access.
type Log struct {
	entries []Entry // index lsn-1, so entries[0] has lsn 1
}

// New returns an empty log.
func New() *Log { return &Log{} }

// Append records key=val at the next lsn and returns the stored entry.
func (l *Log) Append(key, val string) Entry {
	e := Entry{Key: key, Val: val, LSN: len(l.entries) + 1}
	l.entries = append(l.entries, e)
	return e
}

// Range returns entries whose lsn is in (after, through], in lsn order.
// Location is computed directly from lsn; the number of entries examined
// is through-after, independent of the total log length.
func (l *Log) Range(after, through int) []Entry {
	if after < 0 {
		after = 0
	}
	if through > len(l.entries) {
		through = len(l.entries)
	}
	if through <= after || after >= len(l.entries) {
		return nil
	}
	return l.entries[after:through]
}

// Len is the highest assigned lsn (number of appends so far).
func (l *Log) Len() int { return len(l.entries) }
