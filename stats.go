package ontology

import "errors"

// ErrEmpty is returned by Min and Max on an empty set.
var ErrEmpty = errors.New("ontology: empty set")

// Count returns the number of members of the set. It sums one-run lengths
// only, so its cost is O(number of runs), never O(number of bits).
func (b *Bitmap) Count() uint64 {
	n, _ := b.CountEx()
	return n
}

// CountEx is Count plus instrumentation: runsVisited is how many runs the
// computation inspected.
func (b *Bitmap) CountEx() (count uint64, runsVisited int) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, r := range b.runs {
		runsVisited++
		if r.one {
			count += r.length
		}
	}
	return count, runsVisited
}

// Min returns the smallest member of the set, or ErrEmpty if the set is
// empty. It inspects runs from the front only: O(number of runs) worst
// case, O(1) when the first run is a one-run.
func (b *Bitmap) Min() (uint32, error) {
	v, _, err := b.MinEx()
	return v, err
}

// MinEx is Min plus instrumentation: runsVisited is how many runs the
// computation inspected.
func (b *Bitmap) MinEx() (uint32, int, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var start uint64
	for i, r := range b.runs {
		if r.one {
			return uint32(start), i + 1, nil
		}
		start += r.length
	}
	return 0, len(b.runs), ErrEmpty
}

// Max returns the largest member of the set, or ErrEmpty if the set is
// empty. The last run is always a one-run (canonical form), so Max is O(1):
// it inspects exactly one run.
func (b *Bitmap) Max() (uint32, error) {
	v, _, err := b.MaxEx()
	return v, err
}

// MaxEx is Max plus instrumentation: runsVisited is how many runs the
// computation inspected.
func (b *Bitmap) MaxEx() (uint32, int, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	n := len(b.runs)
	if n == 0 {
		return 0, 0, ErrEmpty
	}
	return uint32(b.ends[n-1] - 1), 1, nil
}
