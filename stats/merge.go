package stats

import "math"

// Merge combines two independent accumulators into a new one and returns
// it. Neither input accumulator is modified: Merge takes consistent
// snapshots and builds the result from those, so callers can keep using a
// and b afterwards with bit-identical readings.
//
// The combination uses Chan et al.'s parallel Welford formula:
//
//	n    = n_a + n_b
//	mean = mean_a + (mean_b - mean_a) * n_b / n
//	m2   = m2_a + m2_b + (mean_b - mean_a)^2 * n_a * n_b / n
//
// The result depends only on the union of the samples, not on the order
// in which they were added or merged.
func Merge(a, b *Accumulator) *Accumulator {
	// Snapshot under lock; the inputs are never written, so readings
	// taken before and after Merge are bit-identical.
	sa := a.state()
	sb := b.state()

	// Canonical orientation makes Merge(a,b) and Merge(b,a) execute the
	// same floating-point operations and therefore return bit-identical
	// results. Prefer the side with more samples; break ties on mean and
	// then m2 in a total, order-independent way.
	x, y := sa, sb
	if sa.n < sb.n ||
		(sa.n == sb.n && lessFloat(sa.mean, sb.mean)) ||
		(sa.n == sb.n && sa.mean == sb.mean && lessFloat(sa.m2, sb.m2)) {
		x, y = sb, sa
	}

	out := New()
	out.n = x.n + y.n
	out.skipped = x.skipped + y.skipped
	out.invalid = x.invalid || y.invalid

	if y.n == 0 {
		// Identity: merging in an empty accumulator changes nothing.
		out.mean = x.mean
		out.m2 = x.m2
		return out
	}

	if x.n == 0 {
		// Reached only when both sides are empty (x is canonical).
		out.mean = y.mean
		out.m2 = y.m2
		return out
	}

	na := float64(x.n)
	nb := float64(y.n)
	nt := na + nb
	delta := y.mean - x.mean

	// Parallel Welford combination. The summation order (x terms first,
	// then the cross term, then y terms) is fixed by the canonical
	// orientation above, which is what guarantees bitwise commutativity.
	out.mean = x.mean + delta*nb/nt
	cross := delta * delta * na * nb / nt
	out.m2 = x.m2 + cross + y.m2

	// Defensive: a merged variance is never negative.
	if math.Signbit(out.m2) && !math.IsNaN(out.m2) && !math.IsInf(out.m2, 0) {
		out.m2 = 0
	}
	return out
}

// lessFloat orders float64 values deterministically, including NaN/Inf.
// It is only used to choose a canonical merge orientation on ties.
func lessFloat(u, v float64) bool {
	bu := orderedBits(math.Float64bits(u))
	bv := orderedBits(math.Float64bits(v))
	return bu < bv
}

// orderedBits maps IEEE-754 sign/magnitude bits onto an unsigned integer
// ordering that matches the mathematical float64 ordering.
func orderedBits(b uint64) uint64 {
	if b>>63 != 0 {
		return ^b + 1
	}
	return b ^ (1 << 63)
}
