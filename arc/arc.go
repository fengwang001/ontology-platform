// Package arc is a single-partition append-only archive of committed
// offsets. Entries are ordered by append time (ts non-decreasing);
// eviction removes expired entries from the head only.
package arc

type entry struct {
	off int64
	ts  int64
}

// List is one partition's archive. The zero value is not ready; use New.
type List struct {
	e       []entry
	checked int // unexported: entries inspected by the most recent Evict
}

func New() *List { return &List{} }

// Append records (off, ts). Callers must append with strictly increasing
// off and non-decreasing ts, which keeps entries ordered by both.
func (l *List) Append(off, ts int64) {
	l.e = append(l.e, entry{off: off, ts: ts})
}

// Evict drops head entries whose ts is strictly below cutoff. Because ts
// is non-decreasing, expiry is a prefix: it walks the head, inspecting
// entries one by one, and stops at the first retained entry. The number of
// entries inspected on this call is recorded in the unexported checked
// field (never part of the public surface).
func (l *List) Evict(cutoff int64) {
	l.checked = 0
	n := 0
	for n < len(l.e) {
		l.checked++
		if l.e[n].ts >= cutoff {
			break // first survivor; everything after survives too
		}
		n++
	}
	l.e = l.e[n:]
}

// Max returns the largest surviving offset. Since off strictly increases on
// append, that is always the last surviving entry. ok is false if empty.
func (l *List) Max() (m int64, ok bool) {
	if len(l.e) == 0 {
		return 0, false
	}
	return l.e[len(l.e)-1].off, true
}

func (l *List) Len() int { return len(l.e) }
