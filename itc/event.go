package itc

// evFill fills the event tree along the given identity. Rules are applied in
// the exact order given by the Interval Tree Clock specification.
func evFill(i ID, e Event) Event {
	switch {
	case i.isZero(): // fill(0,e)=e
		return e
	case e.kind == 0: // fill(i,n)=n
		return e
	case i.isOne(): // fill(1,e)=max(e)
		return evInt(evMax(e))
	case i.left.isOne(): // fill((1,ir),(n,el,er))
		er := evFill(*i.right, *e.right)
		return evNode(e.n, evInt(maxInt(evMax(*e.left), evMin(er))), er)
	case i.right.isOne(): // fill((il,1),(n,el,er))
		el := evFill(*i.left, *e.left)
		return evNode(e.n, el, evInt(maxInt(evMax(*e.right), evMin(el))))
	default: // fill((il,ir),(n,el,er))
		return evNode(e.n, evFill(*i.left, *e.left), evFill(*i.right, *e.right))
	}
}

// evGrow grows the event tree along identity i, returning the new tree and
// the cost of the chosen location.
func evGrow(i ID, e Event) (Event, int) {
	if i.isOne() {
		// grow(1,n)=(n+1,0): applies whether the event is an integer or a
		// node; normalized trees with identity 1 are filled to integers.
		return evInt(e.n + 1), 0
	}
	if e.kind == 0 {
		// Integer event with a node identity: expand n to the un-normalized
		// node (n,0,0), recurse, and charge the 1000000 penalty exactly once.
		grown, cost := evGrowNode(i, rawNode(e.n, evInt(0), evInt(0)))
		return grown, cost + 1000000
	}
	return evGrowNode(i, e)
}

func evGrowNode(i ID, e Event) (Event, int) {
	switch {
	case i.left.isZero(): // grow((0,ir),(n,el,er))
		er, cost := evGrow(*i.right, *e.right)
		return evNode(e.n, *e.left, er), cost + 1
	case i.right.isZero(): // grow((il,0),(n,el,er))
		el, cost := evGrow(*i.left, *e.left)
		return evNode(e.n, el, *e.right), cost + 1
	default: // grow((il,ir),(n,el,er))
		el, cl := evGrow(*i.left, *e.left)
		er, cr := evGrow(*i.right, *e.right)
		if cl < cr {
			return evNode(e.n, el, *e.right), cl + 1
		}
		return evNode(e.n, *e.left, er), cr + 1
	}
}

// evJoin merges two event trees.
func evJoin(a, b Event) Event {
	switch {
	case a.kind == 0 && b.kind == 0:
		return evInt(maxInt(a.n, b.n))
	case a.kind == 0:
		return evJoin(rawNode(a.n, evInt(0), evInt(0)), b)
	case b.kind == 0:
		return evJoin(a, rawNode(b.n, evInt(0), evInt(0)))
	default:
		n1, l1, r1 := a.n, a.left, a.right
		n2, l2, r2 := b.n, b.left, b.right
		if n1 > n2 {
			n1, n2 = n2, n1
			l1, l2 = l2, l1
			r1, r2 = r2, r1
		}
		d := n2 - n1
		return evNode(n1, evJoin(*l1, evLift(d, *l2)), evJoin(*r1, evLift(d, *r2)))
	}
}

// evLeq reports whether event tree a is causally before-or-equal to b.
func evLeq(a, b Event) bool {
	switch {
	case a.kind == 0 && b.kind == 0:
		return a.n <= b.n
	case a.kind == 0:
		return a.n <= b.n
	case b.kind == 0:
		return a.n <= b.n &&
			evLeq(evLift(a.n, *a.left), b) &&
			evLeq(evLift(a.n, *a.right), b)
	default:
		return a.n <= b.n &&
			evLeq(evLift(a.n, *a.left), evLift(b.n, *b.left)) &&
			evLeq(evLift(a.n, *a.right), evLift(b.n, *b.right))
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
