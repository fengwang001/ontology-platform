package coverage

import (
	"math/rand"
	"testing"
)

func TestEndpointInclusion(t *testing.T) {
	c := New()
	if err := c.Add(3, 7); err != nil {
		t.Fatal(err)
	}
	if got := c.CountAt(3).Count; got != 1 {
		t.Fatalf("CountAt(lo) = %d, want 1", got)
	}
	if got := c.CountAt(7).Count; got != 0 {
		t.Fatalf("CountAt(hi) = %d, want 0", got)
	}
}

func TestSimultaneousStartAndEnd(t *testing.T) {
	c := New()
	// 两个区间在 10 处一个结束、一个开始；另一个跨越 10。
	_ = c.Add(0, 10)
	_ = c.Add(10, 20)
	_ = c.Add(5, 15)

	// 确定语义：先加后减 -> CountAt(10) 含开始者，不含结束者。
	if got := c.CountAt(10).Count; got != 2 {
		t.Fatalf("CountAt(10) = %d, want 2 (start counted, end not)", got)
	}
	if got := c.CountAt(9).Count; got != 2 {
		t.Fatalf("CountAt(9) = %d, want 2", got)
	}
	if got := c.CountAt(15).Count; got != 1 {
		t.Fatalf("CountAt(15) = %d, want 1", got)
	}

	// 同一组区间反向添加，结果必须逐点一致。
	c2 := New()
	for _, iv := range [][2]int64{{5, 15}, {10, 20}, {0, 10}} {
		_ = c2.Add(iv[0], iv[1])
	}
	for _, p := range []int64{0, 5, 9, 10, 14, 15, 19, 20} {
		if c.CountAt(p).Count != c2.CountAt(p).Count {
			t.Fatalf("CountAt(%d) differs by add order", p)
		}
	}
}

func TestSegmentsOrderIndependentAndMerged(t *testing.T) {
	ivs := [][2]int64{{0, 10}, {10, 20}, {5, 15}, {20, 30}}
	want := []Segment{
		{0, 5, 1},
		{5, 15, 2},
		{15, 30, 1},
	}

	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 8; trial++ {
		order := rng.Perm(len(ivs))
		c := New()
		for _, k := range order {
			if err := c.Add(ivs[k][0], ivs[k][1]); err != nil {
				t.Fatal(err)
			}
		}
		got := c.Segments()
		if len(got) != len(want) {
			t.Fatalf("trial %d: got %v, want %v", trial, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("trial %d seg %d: got %v, want %v", trial, i, got[i], want[i])
			}
		}
	}
}

func TestSegmentsNoZeroAndIndependentCopy(t *testing.T) {
	c := New()
	_ = c.Add(0, 5)
	_ = c.Add(10, 15) // 与前一段之间有 0 覆盖间隙
	segs := c.Segments()
	if len(segs) != 2 {
		t.Fatalf("got %d segments, want 2 (gap must not appear): %v", len(segs), segs)
	}
	for _, s := range segs {
		if s.Count <= 0 {
			t.Fatalf("zero/negative segment: %+v", s)
		}
	}
	segs[0].Count = 999
	again := c.Segments()
	if again[0].Count == 999 {
		t.Fatal("returned slice aliases internal state")
	}
}

func TestCountAtProbesAreLogarithmic(t *testing.T) {
	const n = 20000
	c := New()
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < n; i++ {
		lo := rng.Int63n(1 << 40)
		hi := lo + 1 + rng.Int63n(1000)
		_ = c.Add(lo, hi)
	}

	bound := bits(2*n) + 1
	worst := 0
	for i := 0; i < 100; i++ {
		r := c.CountAt(rng.Int63n(1 << 40))
		if r.EndpointsExamined > bound {
			t.Fatalf("examined %d endpoints, bound %d (not logarithmic?)",
				r.EndpointsExamined, bound)
		}
		if r.EndpointsExamined > worst {
			worst = r.EndpointsExamined
		}
	}
	if worst > bound {
		t.Fatalf("worst probes %d exceed log bound %d", worst, bound)
	}
}

func bits(n int) int {
	b := 0
	for n > 0 {
		n >>= 1
		b++
	}
	if b == 0 {
		return 1
	}
	return b
}
