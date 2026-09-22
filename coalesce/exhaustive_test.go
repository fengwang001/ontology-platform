package coalesce

import (
	"math/rand"
	"testing"

	"ontology/rangespec"
)

// coveredBySpecs expands clipped specs into the exact set of byte offsets
// they request, before any merging happens.
func coveredBySpecs(specs []rangespec.Spec, size int64) map[int64]bool {
	set := make(map[int64]bool)
	for _, sp := range specs {
		r, ok := clip(sp, size)
		if !ok {
			continue
		}
		for i := r.From; i <= r.To; i++ {
			set[i] = true
		}
	}
	return set
}

// coveredByRanges expands normalized ranges into a byte-offset set.
func coveredByRanges(rs []Range) map[int64]bool {
	set := make(map[int64]bool)
	for _, r := range rs {
		for i := r.From; i <= r.To; i++ {
			set[i] = true
		}
	}
	return set
}

func randomSpec(rng *rand.Rand, size int64) rangespec.Spec {
	switch rng.Intn(3) {
	case 0:
		a := rng.Int63n(size + 4)
		b := a + rng.Int63n(size/2+2) - 1 // sometimes inverted (b < a)
		return rangespec.Spec{Kind: rangespec.Span, From: a, To: b}
	case 1:
		return rangespec.Spec{Kind: rangespec.Open, From: rng.Int63n(size + 4)}
	default:
		return rangespec.Spec{Kind: rangespec.Suffix, N: rng.Int63n(size + 4)}
	}
}

// TestByteSetPreserved proves, by exhaustive comparison over randomized
// inputs, that merging never changes the covered byte set: the set of
// bytes covered by the clipped specs equals the set covered by the
// normalized ranges.
func TestByteSetPreserved(t *testing.T) {
	rng := rand.New(rand.NewSource(20260922))
	const size = 64
	for iter := 0; iter < 5000; iter++ {
		n := 1 + rng.Intn(12)
		specs := make([]rangespec.Spec, n)
		for i := range specs {
			specs[i] = randomSpec(rng, size)
		}
		before := coveredBySpecs(specs, size)
		rs, err := Normalize(specs, size)
		if err != nil {
			if len(before) != 0 {
				t.Fatalf("iter %d: unsatisfiable but specs cover %d bytes", iter, len(before))
			}
			continue
		}
		after := coveredByRanges(rs)
		if len(before) != len(after) {
			t.Fatalf("iter %d: |before|=%d |after|=%d specs=%v ranges=%v",
				iter, len(before), len(after), specs, rs)
		}
		for b := range before {
			if !after[b] {
				t.Fatalf("iter %d: byte %d lost by merge; specs=%v ranges=%v",
					iter, b, specs, rs)
			}
		}
		// Postconditions: sorted, non-overlapping, non-adjacent.
		for i := 1; i < len(rs); i++ {
			if rs[i].From <= rs[i-1].To+1 {
				t.Fatalf("iter %d: ranges %v overlap or touch", iter, rs)
			}
		}
	}
}
