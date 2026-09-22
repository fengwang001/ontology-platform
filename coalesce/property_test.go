package coalesce

import (
	"math/rand/v2"
	"testing"

	"ontology/rangespec"
)

func TestNormalizePreservesCoveredByteSet(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	const length = 64
	const trials = 500

	for trial := 0; trial < trials; trial++ {
		items := make([]rangespec.Interval, rng.IntN(12)+1)
		for i := range items {
			switch rng.IntN(4) {
			case 0:
				start := rng.IntN(length + 2)
				items[i] = rangespec.Interval{Start: int64(start), End: int64(start + rng.IntN(8))}
			case 1:
				items[i] = rangespec.Interval{Start: int64(rng.IntN(length + 2)), End: -1}
			case 2:
				items[i] = rangespec.Interval{Start: -1, End: int64(rng.IntN(length + 5))}
			default:
				start := rng.IntN(length)
				items[i] = rangespec.Interval{Start: int64(start), End: int64(rng.IntN(length))}
			}
		}

		want := coveredAfterClipping(items, length)
		got, err := Normalize(items, length)
		if len(want) == 0 {
			if _, ok := IsUnsatisfiable(err); !ok {
				t.Fatalf("trial %d: empty coverage err=%v", trial, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}

		gotSet := map[int64]bool{}
		for i, item := range got {
			if i > 0 && item.Start <= got[i-1].End {
				t.Fatalf("trial %d: overlapping/adjacent output %v", trial, got)
			}
			for off := item.Start; off < item.End; off++ {
				gotSet[off] = true
			}
		}
		if len(gotSet) != len(want) {
			t.Fatalf("trial %d: coverage changed: got %d bytes want %d, ranges=%v", trial, len(gotSet), len(want), got)
		}
		for off := range want {
			if !gotSet[off] {
				t.Fatalf("trial %d: missing offset %d in %v", trial, off, got)
			}
		}
	}
}

func coveredAfterClipping(items []rangespec.Interval, length int64) map[int64]bool {
	covered := map[int64]bool{}
	for _, item := range items {
		var start, end int64
		switch {
		case item.Start >= 0 && item.End >= 0:
			start, end = item.Start, item.End
			if end >= length {
				end = length - 1
			}
		case item.Start >= 0:
			start, end = item.Start, length-1
		case item.End > 0:
			count := item.End
			if count > length {
				count = length
			}
			start, end = length-count, length-1
		default:
			continue
		}
		for off := start; off <= end && off < length; off++ {
			if off >= 0 {
				covered[off] = true
			}
		}
	}
	return covered
}
