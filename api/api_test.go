package api

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

func must(t *testing.T, ok bool) {
	t.Helper()
	if !ok {
		t.Fatal("assertion failed")
	}
}
func do3(g *Merger, o [3]int64) (int64, error) {
	if o[0] == -2 {
		return g.Tick(o[2])
	}
	return g.Report(int(o[0]), o[1], o[2])
}
func TestNineSteps(t *testing.T) {
	g, _ := New(3, 10, 0)
	ops := [][3]int64{{0, 12, 6}, {1, 15, 10}, {0, 40, 12}, {1, 18, 20}, {2, 16, 22}, {2, 21, 26}, {-2, 0, 30}, {-2, 0, 36}, {1, 30, 37}}
	want := []int64{math.MinInt64, 12, 15, 18, 18, 18, 21, 21, 30}
	mask := []int{-1, 1 << 2, -1, -1, 1, -1, -1, 7, -1} // 第 2/5/8 步空闲分区位图
	for i, o := range ops {
		v, e := do3(g, o)
		bm := 0
		for q := 0; q < 3; q++ {
			if g.Idle(q) {
				bm |= 1 << uint(q)
			}
		}
		must(t, e == nil && v == want[i] && (mask[i] < 0 || bm == mask[i]))
	}
}
func TestNaiveRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(20260925))
	for range 200 {
		n := 1 + rng.Intn(6)
		idle := int64(1 + rng.Intn(12))
		g, _ := New(n, idle, 0)
		r := newNaive(n, idle, 0)
		now := int64(0)
		for range 100 {
			p, rep, tn := rng.Intn(n), true, now
			w := r.water[p] + 1 + rng.Int63n(4) // 本分区水位严格递增，永不溢出
			if rng.Intn(5) <= 1 {
				rep, tn = false, now+int64(rng.Intn(int(idle)+4))
			}
			o := [3]int64{int64(p), w, tn}
			if !rep {
				o[0] = -2
			}
			v1, e1 := do3(g, o)
			v2, e2 := r.step(p, w, tn, rep)
			must(t, e1 == e2 && v1 == v2)
			now = tn
		}
	}
}
func TestMonotonic(t *testing.T) {
	cs := []struct {
		n, idle int64
		ops     [][3]int64
		final   int64
	}{
		{2, 5, [][3]int64{{0, 7, 0}, {1, 9, 0}, {-2, 0, 5}, {-2, 0, 100}}, 7},
		{3, 10, [][3]int64{{0, 12, 6}, {1, 15, 10}, {0, 40, 12}, {1, 18, 20}, {2, 16, 22}}, 18},
		{2, 10, [][3]int64{{0, 100, 0}, {1, 200, 1}, {-2, 0, 10}, {0, 150, 10}, {-2, 0, 20}, {0, 300, 20}}, 300},
	}
	for _, c := range cs {
		g, _ := New(int(c.n), c.idle, 0)
		prev := int64(math.MinInt64)
		for _, o := range c.ops {
			v, e := do3(g, o)
			must(t, e == nil && v >= prev)
			prev = v
		}
		must(t, prev == c.final)
	}
}
func TestIdleBoundary(t *testing.T) {
	g, _ := New(1, 10, 0)
	g.Report(0, 5, 0)
	_, e9 := g.Tick(9)
	must(t, e9 == nil && !g.Idle(0))
	_, e10 := g.Tick(10)
	must(t, e10 == nil && g.Idle(0))
	_, er := g.Report(0, 4, 10)
	must(t, errors.Is(er, ErrWatermarkRewind) && g.Idle(0))
	_, e19 := g.Tick(19) // 被拒上报若刷新 last，时差仅 9 会被误判为活跃
	must(t, e19 == nil && g.Idle(0))
}
func TestRejectedNoTrace(t *testing.T) {
	for _, a := range [][2]int64{{0, 1}, {-1, 1}, {1, 0}, {2, -5}} {
		gg, e := New(int(a[0]), a[1], 0)
		must(t, gg == nil && errors.Is(e, ErrInvalidArgument))
	}
	g, _ := New(1, 10, 0)
	g.Report(0, 5, 0)
	bad := [][3]int64{{1, 1, 0}, {-1, 1, 0}, {-2, 0, -1}, {0, 4, 0}} // 末项 p=-2 表示 Tick(-1)
	wantE := []error{ErrPartitionRange, ErrPartitionRange, ErrClockRewind, ErrWatermarkRewind}
	before := g.Merged()
	for i := range bad {
		_, e := do3(g, bad[i])
		must(t, errors.Is(e, wantE[i]) && g.Merged() == before && !g.Idle(0))
	}
	_, e0 := g.Tick(0) // 时钟仍停在 0：相等 now 可用
	_, eBack := g.Tick(-1)
	v, eUse := g.Report(0, 6, 0)
	must(t, e0 == nil && errors.Is(eBack, ErrClockRewind) && eUse == nil && v == 6)
}
func TestConcurrent(t *testing.T) {
	g, _ := New(4, 1<<40, 0)
	r := newNaive(4, 1<<40, 0)
	var bad, running atomic.Bool
	running.Store(true)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			prev := int64(math.MinInt64)
			for running.Load() {
				if v := g.Merged(); v < prev {
					bad.Store(true)
				} else {
					prev = v
				}
			}
		}()
	}
	for i := 0; i < 500; i++ {
		rep := i%5 != 0
		p, w := int64(-2), int64(0)
		if rep {
			p, w = int64(i%4), int64(i)
		}
		o := [3]int64{p, w, int64(i + 1)}
		do3(g, o)
		r.step(int(p), w, int64(i+1), rep)
	}
	running.Store(false)
	wg.Wait()
	must(t, !bad.Load() && g.Merged() == r.merged)
}
