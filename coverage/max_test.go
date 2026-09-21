package coverage

import (
	"math"
	"math/rand"
	"testing"
)

func TestMaxCoverageBasic(t *testing.T) {
	c := New()
	if got := c.MaxCoverage(); got.Found || got.Count != 0 {
		t.Fatalf("empty max = %+v, want zero/not found", got)
	}
	_ = c.Add(0, 100)
	_ = c.Add(50, 100)
	_ = c.Add(50, 60)
	got := c.MaxCoverage()
	if got.Count != 3 || got.Start != 50 {
		t.Fatalf("max = %+v, want count 3 starting at 50", got)
	}

	// 削平最大层后应懒重算并得到正确的新最大值。
	if err := c.Remove(50, 60); err != nil {
		t.Fatal(err)
	}
	got = c.MaxCoverage()
	if got.Count != 2 || got.Start != 50 {
		t.Fatalf("after Remove max = %+v, want 2 @50", got)
	}
	if err := c.Remove(50, 100); err != nil {
		t.Fatal(err)
	}
	got = c.MaxCoverage()
	if got.Count != 1 {
		t.Fatalf("after second Remove max = %+v, want 1", got)
	}

	// 极值区间。
	c2 := New()
	_ = c2.Add(math.MinInt64, math.MaxInt64)
	if m := c2.MaxCoverage(); m.Count != 1 || m.Start != math.MinInt64 {
		t.Fatalf("extreme max = %+v", m)
	}
}

// TestRandomFuzz 用独立的朴素模型交叉验证 CountAt、Segments 与 Max。
func TestRandomFuzz(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	c := New()
	type iv struct{ lo, hi int64 }
	var live []iv

	for step := 0; step < 4000; step++ {
		if rng.Intn(3) == 0 && len(live) > 0 {
			k := rng.Intn(len(live))
			v := live[k]
			if err := c.Remove(v.lo, v.hi); err != nil {
				t.Fatalf("step %d Remove: %v", step, err)
			}
			live = append(live[:k], live[k+1:]...)
		} else {
			lo := rng.Int63n(2000) - 1000
			hi := lo + 1 + rng.Int63n(100)
			if err := c.Add(lo, hi); err != nil {
				t.Fatal(err)
			}
			live = append(live, iv{lo, hi})
		}

		if step%200 != 0 {
			continue
		}
		if err := c.Verify(); err != nil {
			t.Fatalf("step %d Verify: %v", step, err)
		}
		// 在一批采样点上与朴素计数对照。
		for _, p := range []int64{-1100, -1000, -500, 0, 7, 42, 999, 1100} {
			var want int64
			for _, v := range live {
				if v.lo <= p && p < v.hi {
					want++
				}
			}
			if got := c.CountAt(p).Count; got != want {
				t.Fatalf("step %d CountAt(%d) = %d, want %d", step, p, got, want)
			}
		}
		// Segments 重放后必须与逐点计数一致且覆盖连续。
		segs := c.Segments()
		for _, s := range segs {
			var want int64
			for _, v := range live {
				if v.lo <= s.Lo && s.Lo < v.hi {
					want++
				}
			}
			if want != s.Count {
				t.Fatalf("step %d seg [%d,%d) count %d want %d",
					step, s.Lo, s.Hi, s.Count, want)
			}
		}
		// Max 必须等于朴素最大值。
		best := int64(0)
		for p := int64(-1100); p < 1100; p++ {
			var n int64
			for _, v := range live {
				if v.lo <= p && p < v.hi {
					n++
				}
			}
			if n > best {
				best = n
			}
		}
		if m := c.MaxCoverage(); m.Count != best {
			t.Fatalf("step %d max = %d, want %d", step, m.Count, best)
		}
	}
}
