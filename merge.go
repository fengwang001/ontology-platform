package ontology

// snapshot is a consistent, lock-free copy of an accumulator's state.
type snapshot struct {
	n        uint64
	mean     float64
	m2       float64
	skipped  uint64
	poisoned bool
}

// snapshot copies the accumulator state under the read lock, so the
// result is never a half-updated view.
func (a *Accumulator) snapshot() snapshot {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return snapshot{
		n:        a.n,
		mean:     a.mean,
		m2:       a.m2,
		skipped:  a.skipped,
		poisoned: a.poisoned,
	}
}

// Merge combines two accumulators into a new one whose statistics are
// identical to feeding both sample streams into a single accumulator.
// Neither source is modified.
//
// Merge(a, b) and Merge(b, a) are bit-for-bit identical: the combined
// mean is computed as the symmetric weighted sum
// (meanA*na + meanB*nb)/n and the M2 correction uses delta*delta, and
// IEEE 754 addition, multiplication and negation make every term
// invariant under the swap. Merging with an empty accumulator is an
// exact identity, and merging two empties yields an empty one.
func Merge(a, b *Accumulator) *Accumulator {
	sa := a.snapshot()
	sb := b.snapshot()
	out := &Accumulator{
		n:        sa.n + sb.n,
		skipped:  sa.skipped + sb.skipped,
		poisoned: sa.poisoned || sb.poisoned,
	}
	switch {
	case sa.n == 0:
		out.mean, out.m2 = sb.mean, sb.m2
	case sb.n == 0:
		out.mean, out.m2 = sa.mean, sa.m2
	default:
		na := float64(sa.n)
		nb := float64(sb.n)
		n := na + nb
		delta := sb.mean - sa.mean
		// The explicit float64 conversions force each product to be
		// rounded before the addition, which forbids FMA fusion
		// (Go spec) and keeps the sum commutative bit-for-bit.
		out.mean = (float64(sa.mean*na) + float64(sb.mean*nb)) / n
		out.m2 = sa.m2 + sb.m2 + delta*delta*na*nb/n
	}
	return out
}
