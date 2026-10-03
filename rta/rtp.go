package rta

// sat is the saturation ceiling for intermediate sums. All legitimate
// fixed points for valid inputs stay far below it, so saturation only marks
// provably divergent iteration.
const sat int64 = 1 << 62

func satAdd(x, y int64) int64 {
	if x > sat-y {
		return sat
	}
	return x + y
}

func satMul(x, y int64) int64 {
	if y != 0 && x > sat/y {
		return sat
	}
	return x * y
}

// ceilDiv returns ceil(x/y) for non-negative x and positive y without
// overflow (x is bounded by sat).
func ceilDiv(x, y int64) int64 {
	return (x + y - 1) / y
}

// response computes the response time of task idx in order tasks with the
// given higher-priority set hp. It returns (w, R, ok): the least fixed point
// of w = C+B + sum ceil((w+J_j)/T_j)*C_j, R = w + J_idx, and whether the
// deadline test passed at every inspected value including the initial one.
//
// Every evaluated interference term is counted in *termCount (one per
// higher-priority task per fixed-point step), enabling tests to assert
// exactly how much recomputation an operation performed.
func response(tasks []Task, idx int, hp []int, termCount *uint64) (w int64, r int64, ok bool) {
	t := tasks[idx]
	// Deadline D-J is checked against w directly; with valid inputs J <= D
	// is not required, a negative difference simply rejects immediately.
	var limit int64
	if t.J > t.D {
		return t.C + t.B, t.C + t.B + t.J, false
	}
	limit = t.D - t.J

	w = t.C + t.B
	for {
		if w > limit {
			return w, w + t.J, false
		}
		// Fixed point of w = C+B + interference: the base restarts at
		// C+B every iteration, it does not accumulate on top of w.
		next := t.C + t.B
		for _, h := range hp {
			if termCount != nil {
				*termCount++
			}
			ht := tasks[h]
			num := satAdd(w, ht.J)
			k := ceilDiv(num, ht.T)
			next = satAdd(next, satMul(k, ht.C))
		}
		if next == w {
			return w, w + t.J, true
		}
		w = next
	}
}
