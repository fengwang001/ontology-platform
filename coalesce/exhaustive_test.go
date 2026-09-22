package coalesce_test

import (
	"math/rand/v2"
	"testing"

	"ontology/coalesce"
)

// expand 把一组闭区间展开成被覆盖偏移的位图（小空间内）。
func expand(ivs []coalesce.Interval, universe int) []bool {
	bits := make([]bool, universe)
	for _, iv := range ivs {
		for p := iv.Start; p <= iv.End; p++ {
			bits[p] = true
		}
	}
	return bits
}

func randomClippedIntervals(rng *rand.Rand, n, universe int) []coalesce.Interval {
	out := make([]coalesce.Interval, 0, n)
	for i := 0; i < n; i++ {
		a := rng.IntN(universe)
		b := rng.IntN(universe)
		if a > b {
			a, b = b, a
		}
		out = append(out, coalesce.Interval{Start: int64(a), End: int64(b)})
	}
	return out
}

// TestMergePreservesByteSetExhaustive：合并前后覆盖的字节集合必须完全相同。
// 用大量随机集合 + 位图穷举对照来证明，且合并结果必须互不重叠、互不相邻。
func TestMergePreservesByteSetExhaustive(t *testing.T) {
	const universe = 64
	const iterations = 20000
	rng := rand.New(rand.NewPCG(1, 2))

	for it := 0; it < iterations; it++ {
		n := 1 + rng.IntN(12)
		in := randomClippedIntervals(rng, n, universe)

		nr := coalesce.NewNormalizer()
		specs := toSpecs(in)
		out, err := nr.Normalize(specs, universe)
		if err != nil {
			t.Fatalf("normalize: %v", err)
		}

		before := expand(in, universe)
		after := expand(out, universe)
		for p := 0; p < universe; p++ {
			if before[p] != after[p] {
				t.Fatalf("iter %d: byte %d coverage changed; in=%v out=%v",
					it, p, in, out)
			}
		}

		// 结果必须按起点升序，且既不重叠也不相邻（起点差 > 前一段长度+1）。
		for i := 1; i < len(out); i++ {
			if out[i].Start <= out[i-1].End {
				t.Fatalf("iter %d: overlapping output %v", it, out)
			}
			if out[i].Start == out[i-1].End+1 {
				t.Fatalf("iter %d: adjacent output not merged %v", it, out)
			}
		}
	}
}

func TestAdjacentMustMerge(t *testing.T) {
	// [0,4] 与 [5,9] 首尾相接，必须合并为一个区间。
	in := []coalesce.Interval{{Start: 0, End: 4}, {Start: 5, End: 9}}
	out, err := coalesce.NewNormalizer().Normalize(toSpecs(in), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0] != (coalesce.Interval{Start: 0, End: 9}) {
		t.Fatalf("adjacent not merged: %+v", out)
	}
}
