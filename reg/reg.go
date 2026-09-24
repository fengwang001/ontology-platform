// Package reg holds the per-key state of the first-write-wins dedup register.
//
// A Register knows only the currently effective (Seq, Val) of one key; it has
// no knowledge of other keys, changelogs or counters.
package reg

import "errors"

// ErrDuplicateSeq is returned when a write carries the same Seq as the
// currently materialized entry for the key.
var ErrDuplicateSeq = errors.New("reg: seq duplicates the effective seq for the key")

// Entry is one materialized write.
type Entry struct {
	Seq int64
	Val string
}

// Register is the materialized state of a single key.
type Register struct {
	cur    Entry
	exists bool
}

// New returns an empty register.
func New() *Register { return &Register{} }

// Clone returns an independent copy.
func (r *Register) Clone() *Register { c := *r; return &c }

// Materialized reports whether a value is currently effective.
func (r *Register) Materialized() bool { return r.exists }

// Current returns the effective entry; valid only when Materialized.
func (r *Register) Current() Entry { return r.cur }

// Outcome tells the caller what Step did.
type Outcome struct {
	Had     bool  // an entry was materialized before the write
	Old     Entry // that previously materialized entry (valid when Had)
	Added   bool  // the new write becomes effective (emit a "+")
	Dropped bool  // the write is a late write with a larger Seq
}

// Step applies one write (seq, val) to the register:
//
//   - empty register: the write becomes effective (Added).
//   - seq < current Seq: the old entry is withdrawn, the new one added.
//   - seq > current Seq: the late write is dropped.
//   - seq == current Seq: ErrDuplicateSeq, state untouched.
func (r *Register) Step(seq int64, val string) (Outcome, error) {
	if !r.exists {
		r.cur = Entry{Seq: seq, Val: val}
		r.exists = true
		return Outcome{Added: true}, nil
	}
	switch {
	case seq < r.cur.Seq:
		old := r.cur
		r.cur = Entry{Seq: seq, Val: val}
		return Outcome{Had: true, Old: old, Added: true}, nil
	case seq > r.cur.Seq:
		return Outcome{Dropped: true}, nil
	default:
		return Outcome{}, ErrDuplicateSeq
	}
}
