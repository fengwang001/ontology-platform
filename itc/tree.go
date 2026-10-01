package itc

import "strconv"

// Identity-tree constructors. They return the unique normalized leaf for
// (0,0) and (1,1), so every ID in use is normalized.
func idZero() ID { return ID{kind: 0} }
func idOne() ID  { return ID{kind: 1} }

func idPair(l, r ID) ID {
	if l.kind == 0 && r.kind == 0 {
		return idZero()
	}
	if l.kind == 1 && r.kind == 1 {
		return idOne()
	}
	return ID{kind: 2, left: &l, right: &r}
}

func (i ID) isZero() bool { return i.kind == 0 }
func (i ID) isOne() bool  { return i.kind == 1 }
func (i ID) isPair() bool { return i.kind == 2 }

func (i ID) String() string {
	switch i.kind {
	case 0:
		return "0"
	case 1:
		return "1"
	default:
		return "(" + i.left.String() + "," + i.right.String() + ")"
	}
}

// idFork splits an identity into two disjoint identities.
func idFork(i ID) (ID, ID) {
	switch {
	case i.isZero():
		return idZero(), idZero()
	case i.isOne():
		return idPair(idOne(), idZero()), idPair(idZero(), idOne())
	case i.left.isZero():
		l, r := idFork(*i.right)
		return idPair(idZero(), l), idPair(idZero(), r)
	case i.right.isZero():
		l, r := idFork(*i.left)
		return idPair(l, idZero()), idPair(r, idZero())
	default:
		return idPair(*i.left, idZero()), idPair(idZero(), *i.right)
	}
}

// idSum merges two identities. ok is false when the identities overlap.
func idSum(a, b ID) (ID, bool) {
	switch {
	case a.isZero():
		return b, true
	case b.isZero():
		return a, true
	case a.isPair() && b.isPair():
		l, okL := idSum(*a.left, *b.left)
		if !okL {
			return idZero(), false
		}
		r, okR := idSum(*a.right, *b.right)
		if !okR {
			return idZero(), false
		}
		return idPair(l, r), true
	default:
		return idZero(), false
	}
}

// Event-tree constructors. evNode performs event-tree normalization.
func evInt(n int) Event { return Event{kind: 0, n: n} }

// rawNode builds an un-normalized event node. It is only used internally
// where the specification explicitly operates on a non-normalized shape
// (growing an integer event, and join's integer-as-node promotion); the
// caller normalizes the final result.
func rawNode(n int, l, r Event) Event {
	return Event{kind: 1, n: n, left: &l, right: &r}
}

func evNode(n int, l, r Event) Event {
	if l.kind == 0 && r.kind == 0 && l.n == r.n {
		return evInt(n + l.n)
	}
	m := minEvent(l, r)
	if m != 0 {
		sl := evSub(l, m)
		sr := evSub(r, m)
		return Event{kind: 1, n: n + m, left: &sl, right: &sr}
	}
	return Event{kind: 1, n: n, left: &l, right: &r}
}

func minEvent(a, b Event) int {
	x := evMin(a)
	y := evMin(b)
	if x < y {
		return x
	}
	return y
}

func evMin(e Event) int {
	if e.kind == 0 {
		return e.n
	}
	l := evMin(*e.left)
	r := evMin(*e.right)
	if l < r {
		return e.n + l
	}
	return e.n + r
}

func evMax(e Event) int {
	if e.kind == 0 {
		return e.n
	}
	l := evMax(*e.left)
	r := evMax(*e.right)
	if l > r {
		return e.n + l
	}
	return e.n + r
}

// evLift adds m to the base component of an event tree.
func evLift(m int, e Event) Event {
	if e.kind == 0 {
		return evInt(e.n + m)
	}
	return Event{kind: 1, n: e.n + m, left: e.left, right: e.right}
}

// evSub subtracts m from the base component of an event tree.
func evSub(e Event, m int) Event {
	return evLift(-m, e)
}

func (e Event) String() string {
	if e.kind == 0 {
		return strconv.Itoa(e.n)
	}
	return "(" + strconv.Itoa(e.n) + "," + e.left.String() + "," + e.right.String() + ")"
}

func evEqual(a, b Event) bool {
	if a.kind != b.kind || a.n != b.n {
		return false
	}
	if a.kind == 0 {
		return true
	}
	return evEqual(*a.left, *b.left) && evEqual(*a.right, *b.right)
}

func evClone(e Event) Event {
	if e.kind == 0 {
		return evInt(e.n)
	}
	l := evClone(*e.left)
	r := evClone(*e.right)
	return Event{kind: 1, n: e.n, left: &l, right: &r}
}
