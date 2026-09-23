// Package slots is the capacity ledger: every live slot is in exactly
// one of Idle, Lent, Creating, Destroying, and the ledger keeps
// per-state counts plus a self-check. It owns no resources and knows
// nothing about waiting or eviction.
package slots

import "errors"

type State uint8

const (
	Idle State = iota
	Lent
	Creating
	Destroying
)

// Ledger tracks slot states and generations up to a capacity max.
// Not safe for concurrent use; the pool serializes access.
type Ledger struct {
	max  int
	st   map[int]State
	gen  map[int]uint64
	free []int
	next int
	cnt  [4]int
}

func New(max int) *Ledger {
	return &Ledger{max: max, st: make(map[int]State), gen: make(map[int]uint64)}
}

// CanCreate reports whether Idle+Lent+Creating is below max.
func (l *Ledger) CanCreate() bool { return l.cnt[Idle]+l.cnt[Lent]+l.cnt[Creating] < l.max }

// BeginCreate reserves a slot in Creating state and returns its number.
func (l *Ledger) BeginCreate() int {
	s := l.next
	l.next++
	if n := len(l.free); n > 0 {
		s = l.free[n-1]
		l.free = l.free[:n-1]
	}
	l.st[s] = Creating
	l.cnt[Creating]++
	return s
}

// AbortCreate drops a Creating slot whose Create failed.
func (l *Ledger) AbortCreate(s int) { l.remove(s, Creating) }

// MarkLent moves a slot (from Creating, Idle or Lent on handoff) to Lent
// and returns the new generation.
func (l *Ledger) MarkLent(s int) uint64 {
	l.cnt[l.st[s]]--
	l.st[s] = Lent
	l.cnt[Lent]++
	l.gen[s]++
	return l.gen[s]
}

func (l *Ledger) MarkIdle(s int) { l.cnt[Lent]--; l.st[s] = Idle; l.cnt[Idle]++ }

// IsLent reports whether the slot is lent out under exactly this generation.
func (l *Ledger) IsLent(s int, g uint64) bool { return l.st[s] == Lent && l.gen[s] == g }

func (l *Ledger) BeginDestroy(s int) { l.cnt[l.st[s]]--; l.st[s] = Destroying; l.cnt[Destroying]++ }

// Destroyed retires a Destroying slot; its number may be reused, but the
// generation keeps increasing so old handles stay stale.
func (l *Ledger) Destroyed(s int) { l.remove(s, Destroying) }

func (l *Ledger) remove(s int, from State) {
	l.cnt[from]--
	delete(l.st, s)
	l.free = append(l.free, s)
}

func (l *Ledger) Counts() (idle, lent, creating, destroying int) {
	return l.cnt[0], l.cnt[1], l.cnt[2], l.cnt[3]
}

// Check verifies the counts against the per-slot records and capacity.
func (l *Ledger) Check() error {
	var c [4]int
	for _, s := range l.st {
		c[s]++
	}
	if c != l.cnt {
		return errors.New("slots: count mismatch")
	}
	if c[Idle]+c[Lent]+c[Creating] > l.max {
		return errors.New("slots: over capacity")
	}
	return nil
}
