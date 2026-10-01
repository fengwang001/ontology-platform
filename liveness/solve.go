package liveness

import "sort"

// sealLocked validates successor references and computes the least fixpoint.
// Callers must hold a.mu.
func (a *Analyzer) sealLocked() error {
	if a.sealed {
		return ErrSealedTwice
	}
	if len(a.order) == 0 {
		return ErrNoBlocks
	}

	// First missing successor: blocks visited by ascending id, successors in
	// their recorded order.
	ids := append([]int(nil), a.order...)
	sort.Ints(ids)
	for _, id := range ids {
		b := a.blocks[id]
		for _, succ := range b.spec.Successors {
			if _, ok := a.blocks[succ]; !ok {
				return &MissingSuccessorError{BlockID: id, Successor: succ}
			}
		}
	}

	a.solveLocked()
	a.sealed = true
	return nil
}

// solveLocked computes the least fixpoint of:
//
//	out[B] = union of in[S] over successors S (empty when none)
//	in[B]  = ue[B] union (out[B] minus def[B])
//
// Iteration starts from the empty set and only ever grows sets, so the
// reached fixpoint is the least one. A reverse-postorder worklist makes it
// converge quickly while the result is independent of iteration order.
func (a *Analyzer) solveLocked() {
	preds := make(map[int][]int, len(a.order))
	for _, id := range a.order {
		b := a.blocks[id]
		for _, succ := range b.spec.Successors {
			preds[succ] = append(preds[succ], id)
		}
	}

	work := make(map[int]struct{}, len(a.order))
	var stack []int
	push := func(id int) {
		if _, queued := work[id]; queued {
			return
		}
		work[id] = struct{}{}
		stack = append(stack, id)
	}
	pop := func() int {
		n := len(stack) - 1
		id := stack[n]
		stack = stack[:n]
		delete(work, id)
		return id
	}

	// Seed in reverse insertion order as a deterministic starting queue;
	// convergence itself is order-independent.
	for i := len(a.order) - 1; i >= 0; i-- {
		push(a.order[i])
	}

	for len(stack) > 0 {
		id := pop()
		b := a.blocks[id]

		newOut := newSet()
		for _, succ := range b.spec.Successors {
			setUnion(newOut, a.blocks[succ].in)
		}
		newIn := setCopy(b.ue)
		outMinusDef := setCopy(newOut)
		setSubtract(outMinusDef, b.def)
		setUnion(newIn, outMinusDef)

		if !setEqual(newIn, b.in) || !setEqual(newOut, b.out) {
			b.in = newIn
			b.out = newOut
			for _, p := range preds[id] {
				push(p)
			}
		}
	}
}
