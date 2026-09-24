// Package hlc implements a single-node Hybrid Logical Clock:
// the local/send rule, the receive rule, and the offset/counter limits.
package hlc

import "errors"

// Decidable sentinel errors.
var (
	ErrOffset  = errors.New("hlc: message offset exceeds maxOffset")
	ErrCounter = errors.New("hlc: counter exceeds maxC")
)

// T is an HLC timestamp, ordered lexicographically by (L, C).
type T struct{ L, C int64 }

// LE reports whether t <= o in (L, C) lexicographic order.
func (t T) LE(o T) bool { return t.L < o.L || (t.L == o.L && t.C <= o.C) }

// Clock is one node's HLC. The zero value plus maxC is created via New.
type Clock struct {
	l, c int64
	maxC int64
}

// New returns a clock at (0,0) rejecting any event with c > maxC.
func New(maxC int64) *Clock { return &Clock{maxC: maxC} }

// Now returns the current timestamp without advancing the clock.
func (k *Clock) Now() T { return T{k.l, k.c} }

// Tick applies the local/send rule with physical reading pt.
func (k *Clock) Tick(pt int64) (T, error) {
	l, c := k.l, k.c
	lp := l
	l = max(l, pt)
	if l == lp {
		c++
	} else {
		c = 0
	}
	return k.commit(l, c)
}

// Receive applies the receive rule for a message carrying m, read at pt.
func (k *Clock) Receive(pt int64, m T, maxOffset int64) (T, error) {
	if m.L-pt > maxOffset {
		return T{}, ErrOffset
	}
	l, c := k.l, k.c
	lp := l
	l = max(l, m.L, pt)
	switch {
	case l == lp && l == m.L:
		c = max(c, m.C) + 1
	case l == lp:
		c++
	case l == m.L:
		c = m.C + 1
	default:
		c = 0
	}
	return k.commit(l, c)
}

// commit stores (l,c) only after every check has passed, so a rejected
// event never mutates the clock.
func (k *Clock) commit(l, c int64) (T, error) {
	if c > k.maxC {
		return T{}, ErrCounter
	}
	k.l, k.c = l, c
	return T{l, c}, nil
}
