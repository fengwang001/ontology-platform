package sdot

import (
	"math"
	"math/rand"
	"sync"
	"testing"

	"ontology/spv"
)

// denseRef 是朴素参照：物化成 m 维稠密数组后逐项乘加（测试专用）。
func denseRef(m int, a, b *spv.Vec) float64 {
	sum := 0.0
	for k := 0; k < m; k++ {
		sum += a.Get(k) * b.Get(k)
	}
	return sum
}

// setInOrder 以给定到达顺序写入 (idx,val)，用于模拟随机到达。
func setInOrder(t *testing.T, v *spv.Vec, idx []int, val []float64, order []int) {
	t.Helper()
	for _, k := range order {
		if err := v.Set(idx[k], val[k]); err != nil {
			t.Fatalf("Set(%d,%v): %v", idx[k], val[k], err)
		}
	}
}

func TestDotMatchesDense(t *testing.T) {
	// 表驱动固定用例：负元素、不相交、空向量、完全重合。
	fixed := []struct {
		m      int
		ai, bi []int
		av, bv []float64
		want   float64
	}{
		{10, []int{1, 3, 5}, []int{0, 1, 5}, []float64{2, 3, 5}, []float64{4, 7, 6}, 44},
		{6, []int{0, 2, 4}, []int{0, 2, 3}, []float64{-1.5, 2, -3}, []float64{4, -2, 10}, -10},
		{8, []int{0, 2}, []int{1, 3}, []float64{5, -5}, []float64{5, -5}, 0},
		{4, nil, []int{1, 2}, nil, []float64{9, 9}, 0},
		{4, []int{1, 2}, []int{1, 2}, []float64{3, 4}, []float64{3, 4}, 25},
		{4, []int{0, 1}, []int{0, 1}, []float64{-1, -1}, []float64{-1, -1}, 2},
	}
	for _, c := range fixed {
		a, b := spv.New(c.m), spv.New(c.m)
		setInOrder(t, a, c.ai, c.av, perm(len(c.ai)))
		setInOrder(t, b, c.bi, c.bv, perm(len(c.bv)))
		if got := Dot(a, b); got != c.want || got != denseRef(c.m, a, b) {
			t.Errorf("m=%d: Dot=%v want %v (dense %v)", c.m, got, c.want, denseRef(c.m, a, b))
		}
	}
	// 随机用例：随机 m、随机非零数、随机到达顺序；负/正元素混合。
	pool := []float64{-3, -2, -1, -0.5, 0.5, 1, 2, 3}
	dims := []int{1, 2, 3, 7, 17, 64}
	for seed := int64(0); seed < 50; seed++ {
		rng := rand.New(rand.NewSource(seed))
		m := dims[int(seed)%len(dims)]
		a, b := spv.New(m), spv.New(m)
		fillRand := func(v *spv.Vec) {
			pos := rng.Perm(m)[:rng.Intn(m+1)]
			ix, vals, order := make([]int, len(pos)), make([]float64, len(pos)), rng.Perm(len(pos))
			for k, p := range pos {
				ix[k] = p
				vals[k] = pool[rng.Intn(len(pool))]
			}
			setInOrder(t, v, ix, vals, order)
		}
		fillRand(a)
		fillRand(b)
		if got, want := Dot(a, b), denseRef(m, a, b); got != want {
			t.Errorf("seed=%d m=%d: Dot=%v dense=%v", seed, m, got, want)
		}
	}
}

func TestAccessCounterIndependentOfM(t *testing.T) {
	// 固定例：3+3 个条目，访问数恒为 6（含尾部排空）。
	a, b := spv.New(10), spv.New(10)
	setInOrder(t, a, []int{1, 3, 5}, []float64{2, 3, 5}, []int{0, 1, 2})
	setInOrder(t, b, []int{0, 1, 5}, []float64{4, 7, 6}, []int{0, 1, 2})
	if d, seen := dotAccesses(a, b); d != 44 || seen != 6 {
		t.Fatalf("example: dot=%v seen=%v want 44,6", d, seen)
	}
	// 多档 m：两个各 10 个非零条目的向量，下标在 [0,m) 内随机分布。
	for _, m := range []int{100, 1000, 10000} {
		for seed := int64(0); seed < 10; seed++ {
			rng := rand.New(rand.NewSource(seed + int64(m)))
			x, y := spv.New(m), spv.New(m)
			fillTen := func(v *spv.Vec, tag int64) {
				r := rand.New(rand.NewSource(tag))
				pos := r.Perm(m)[:10]
				vals := make([]float64, 10)
				for k := range vals {
					vals[k] = float64(r.Intn(7) - 3)
					if vals[k] == 0 {
						vals[k] = 1
					}
				}
				setInOrder(t, v, pos, vals, rng.Perm(10))
			}
			fillTen(x, seed*2+1)
			fillTen(y, seed*2+2)
			d, seen := dotAccesses(x, y)
			xi, _ := x.Snapshot()
			yi, _ := y.Snapshot()
			if len(xi) != 10 || len(yi) != 10 {
				t.Fatalf("m=%d seed=%d: nnz=%d,%d want 10,10", m, seed, len(xi), len(yi))
			}
			if seen > 20 || seen != int64(len(xi)+len(yi)) {
				t.Errorf("m=%d seed=%d: accessed=%d want exactly 20", m, seed, seen)
			}
			if d != denseRef(m, x, y) {
				t.Errorf("m=%d seed=%d: dot=%v dense=%v", m, seed, d, denseRef(m, x, y))
			}
		}
	}
}

func TestConcurrentDotSameResult(t *testing.T) {
	a, b := spv.New(10), spv.New(10)
	setInOrder(t, a, []int{1, 3, 5}, []float64{2, 3, 5}, []int{2, 0, 1})
	setInOrder(t, b, []int{0, 1, 5}, []float64{4, 7, 6}, []int{2, 1, 0})
	want := math.Float64bits(Dot(a, b))
	const n = 128
	results := make([]uint64, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for g := 0; g < n; g++ {
		go func(g int) { defer wg.Done(); results[g] = math.Float64bits(Dot(a, b)) }(g)
	}
	wg.Wait()
	for g, bits := range results {
		if bits != want {
			t.Fatalf("goroutine %d: bits=%016x want %016x", g, bits, want)
		}
	}
}

// perm 返回 [0..n) 的确定性顺序；n=0 时返回 nil。
func perm(n int) []int {
	p := make([]int, n)
	for k := range p {
		p[k] = k
	}
	return p
}
