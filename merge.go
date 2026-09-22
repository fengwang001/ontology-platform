package ontology

import "math"

// snapshot is an immutable copy of an Accumulator's state.
type snapshot struct {
	count    int64
	mean     float64
	m2       float64
	skipped  int64
	poisoned bool
}

// Merge returns a new Accumulator that represents the union of the
// samples in a and b. Neither a nor b is modified.
//
// The combination uses the parallel variance formula of Chan,
// Golub & LeVeque (1979). For groups A and B with counts na, nb,
// means ma, mb and M2 values Ma, Mb:
//
//	n     = na + nb
//	delta = mb - ma
//	mean  = ma + delta*nb/n
//	M2    = Ma + Mb + delta^2 * na*nb/n
//
// Merge is commutative bit-for-bit: Merge(a, b) and Merge(b, a)
// produce identical bits, because the two operands are first placed
// in a canonical total order (by count, then by mean bits, then by
// M2 bits) before the formula is applied.
//
// Merging with an empty accumulator is an identity operation: the
// result's statistics are bit-for-bit equal to the non-empty side.
// Merging two empty accumulators yields an empty accumulator.
func Merge(a, b *Accumulator) *Accumulator {
	sa, sb := a.snapshot(), b.snapshot()
	if lessSnapshot(sb, sa) {
		sa, sb = sb, sa
	}
	out := &Accumulator{
		skipped:  sa.skipped + sb.skipped,
		poisoned: sa.poisoned || sb.poisoned,
	}
	if sa.count == 0 {
		// Identity: copy the other side bit-for-bit.
		out.count = sb.count
		out.mean = sb.mean
		out.m2 = sb.m2
		return out
	}
	n := sa.count + sb.count
	delta := sb.mean - sa.mean
	out.count = n
	out.mean = sa.mean + delta*float64(sb.count)/float64(n)
	out.m2 = sa.m2 + sb.m2 + delta*delta*float64(sa.count)*float64(sb.count)/float64(n)
	return out
}

// lessSnapshot defines a deterministic total order on snapshots so
// that Merge computes the same sequence of float operations
// regardless of argument order. Bit patterns are compared (not
// values) so that -0/+0 and NaN order deterministically too.
func lessSnapshot(x, y snapshot) bool {
	if x.count != y.count {
		return x.count < y.count
	}
	if bx, by := math.Float64bits(x.mean), math.Float64bits(y.mean); bx != by {
		return bx < by
	}
	return math.Float64bits(x.m2) < math.Float64bits(y.m2)
}
