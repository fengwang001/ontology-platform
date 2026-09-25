// Package cell implements last-writer-wins resolution for a single column.
package cell

// Cell is the current winner of one (Key, Col): either a value or a
// tombstone, together with its timestamp.
type Cell struct {
	TS      int64
	Val     string
	Tomb    bool
	Present bool // false until the first write is absorbed
}

// Apply merges an incoming write (value or tombstone) into c and reports
// whether a conflict occurred. A conflict is a timestamp tie between
// non-identical writes (two different values, or value vs tombstone).
//
// Rules: higher TS wins; lower TS is ignored; on a tie, tombstone beats
// value and the lexicographically larger value beats the smaller.
func (c *Cell) Apply(ts int64, val string, tomb bool) (conflict bool) {
	if !c.Present {
		c.TS, c.Val, c.Tomb, c.Present = ts, val, tomb, true
		return false
	}
	switch {
	case ts > c.TS:
		c.TS, c.Val, c.Tomb = ts, val, tomb
	case ts < c.TS:
		// Stale write, ignored regardless of arrival order.
	default: // tie
		switch {
		case c.Tomb && tomb:
			// Two tombstones are equivalent: no change, no conflict.
		case c.Tomb != tomb:
			conflict = true
			if tomb { // tombstone beats value
				c.Val, c.Tomb = "", true
			}
		case val != c.Val: // two values: larger wins
			conflict = true
			if val > c.Val {
				c.Val = val
			}
		}
	}
	return conflict
}
