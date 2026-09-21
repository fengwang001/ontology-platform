package ontology

import "math"

// snapshot is a consistent, lock-free copy of an accumulator's state.
type snapshot struct {
	n       int64
	mean    float64
	m2      float64
	skipped int64
	bad     bool
}

// snapshot copies the accumulator state under its lock, so readers
// never observe a half-updated (count, mean, m2) triple.
func (a *Accumulator) snapshot() snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	return snapshot{n: a.n, mean: a.mean, m2: a.m2, skipped: a.skipped, bad: a.bad}
}

// addLocked applies Welford's recurrence for one sample.
// Callers must hold a.mu.
func (a *Accumulator) addLocked(x float64) {
	if math.IsNaN(x) {
		a.skipped++
		return
	}
	if math.IsInf(x, 0) {
		a.bad = true
		return
	}
	a.n++
	delta := x - a.mean
	a.mean += delta / float64(a.n)
	a.m2 += delta * (x - a.mean)
}

// meanLocked returns the running mean.
// Callers must hold a.mu.
func (a *Accumulator) meanLocked() (float64, error) {
	if a.bad {
		return 0, ErrStatsUnavailable
	}
	if a.n == 0 {
		return 0, ErrNoSamples
	}
	return a.mean, nil
}

// populationVarianceLocked returns m2/n.
// Callers must hold a.mu.
func (a *Accumulator) populationVarianceLocked() (float64, error) {
	if a.bad {
		return 0, ErrStatsUnavailable
	}
	if a.n == 0 {
		return 0, ErrNoSamples
	}
	return nonNegative(a.m2 / float64(a.n)), nil
}

// sampleVarianceLocked returns m2/(n-1).
// Callers must hold a.mu.
func (a *Accumulator) sampleVarianceLocked() (float64, error) {
	if a.bad {
		return 0, ErrStatsUnavailable
	}
	if a.n == 0 {
		return 0, ErrNoSamples
	}
	if a.n == 1 {
		return 0, ErrDegenerateFreedom
	}
	return nonNegative(a.m2 / float64(a.n-1)), nil
}

// nonNegative clamps tiny negative roundoff to exact zero. Variance
// is mathematically non-negative; m2 can only dip below zero through
// floating-point cancellation on nearly constant input.
func nonNegative(v float64) float64 {
	if v < 0 {
		return 0
	}
	return v
}

// mergeSnapshots combines two snapshots with Chan's parallel
// algorithm. To make Merge bitwise commutative, operands are placed
// in a canonical order before combining: the empty side is the
// identity, and otherwise the snapshot with the smaller mean (ties
// broken by count, then m2) is treated as "a".
func mergeSnapshots(sa, sb snapshot) *Accumulator {
	if sa.n == 0 {
		return fromSnapshot(sb)
	}
	if sb.n == 0 {
		return fromSnapshot(sa)
	}
	if !orderedBefore(sa, sb) {
		sa, sb = sb, sa
	}
	n := sa.n + sb.n
	delta := sb.mean - sa.mean
	mean := sa.mean + delta*float64(sb.n)/float64(n)
	m2 := sa.m2 + sb.m2 + delta*delta*float64(sa.n)*float64(sb.n)/float64(n)
	return &Accumulator{
		n:       n,
		mean:    mean,
		m2:      m2,
		skipped: sa.skipped + sb.skipped,
		bad:     sa.bad || sb.bad,
	}
}

// orderedBefore reports whether sa should be the left operand in the
// canonical merge order.
func orderedBefore(sa, sb snapshot) bool {
	if sa.mean != sb.mean {
		return sa.mean < sb.mean
	}
	if sa.n != sb.n {
		return sa.n < sb.n
	}
	return sa.m2 <= sb.m2
}

// fromSnapshot rebuilds an Accumulator from a snapshot.
func fromSnapshot(s snapshot) *Accumulator {
	return &Accumulator{n: s.n, mean: s.mean, m2: s.m2, skipped: s.skipped, bad: s.bad}
}
