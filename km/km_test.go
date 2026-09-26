package km

import (
	"errors"
	"math"
	"math/rand"
	"slices"
	"testing"

	"ontology/bmg"
)

// brute 枚举全部 n! 个排列，返回最大总权及其中字典序最小的匹配。
func brute(n int, w [][]int64) (int64, []int) {
	best, p, bestP := int64(math.MinInt64), make([]int, n), []int(nil)
	for i := range p {
		p[i] = i
	}
	var rec func(k int)
	rec = func(k int) {
		if k == n {
			var s int64
			for i, j := range p {
				s += w[i][j]
			}
			if s > best || s == best && (bestP == nil || slices.Compare(p, bestP) < 0) {
				best, bestP = s, append([]int(nil), p...)
			}
			return
		}
		for i := k; i < n; i++ {
			p[k], p[i] = p[i], p[k]
			rec(k + 1)
			p[k], p[i] = p[i], p[k]
		}
	}
	rec(0)
	return best, bestP
}

func graphFrom(n int, w [][]int64) *bmg.Graph {
	g, _ := bmg.New(n) // 测试辅助：n>0、下标合法且边不重复，必成功
	for i := range w {
		for j := range w[i] {
			_ = g.SetWeight(i, j, w[i][j])
		}
	}
	return g
}

func randW(n, seed int) [][]int64 {
	rng := rand.New(rand.NewSource(int64(seed)))
	w := make([][]int64, n)
	for i := range w {
		w[i] = make([]int64, n)
		for j := range w[i] {
			w[i][j] = rng.Int63n(41) - 20 // [-20,20]，含负权与 0
		}
	}
	return w
}

func TestSolveMatchesBruteForce(t *testing.T) {
	for _, c := range []struct{ n, seed int }{{1, 1}, {2, 2}, {3, 3}, {4, 4}, {5, 5}, {6, 6}, {6, 17}} {
		w := randW(c.n, c.seed)
		wantV, wantM := brute(c.n, w)
		gotV, gotM, err := New().Solve(graphFrom(c.n, w))
		if err != nil {
			t.Fatalf("n=%d: %v", c.n, err)
		}
		if gotV != wantV || !slices.Equal(gotM, wantM) {
			t.Fatalf("n=%d seed=%d: got (%d,%v), want (%d,%v)", c.n, c.seed, gotV, gotM, wantV, wantM)
		}
	}
}

func TestSectionThreeMatrix(t *testing.T) {
	w := [][]int64{{10, 8, 0}, {10, 0, 0}, {0, 10, 10}}
	v, m, err := New().Solve(graphFrom(3, w))
	if err != nil || v != 28 || !slices.Equal(m, []int{1, 0, 2}) {
		t.Fatalf("got (%d,%v,%v), want 28 [1 0 2]", v, m, err)
	}
}

func TestLexicographicallySmallest(t *testing.T) {
	for _, n := range []int{1, 2, 3, 4, 5, 6} {
		w := make([][]int64, n) // 全部并列：任何完美匹配等价
		for i := range w {
			w[i] = make([]int64, n)
		}
		_, m, err := New().Solve(graphFrom(n, w))
		if err != nil {
			t.Fatal(err)
		}
		want := make([]int, n)
		for i := range want {
			want[i] = i
		}
		if !slices.Equal(m, want) {
			t.Fatalf("n=%d m=%v, want 恒等排列", n, m)
		}
	}
}

func TestIncomplete(t *testing.T) {
	g, _ := bmg.New(2)
	_ = g.SetWeight(0, 0, 1)
	if _, _, err := New().Solve(g); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("err=%v, want ErrIncomplete", err)
	}
}

func TestDeltaRightChecksBounded(t *testing.T) {
	for _, n := range []int{100, 500, 1000, 5000, 10000} {
		g, _ := bmg.New(n)
		const M = int64(1_000_000)
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				if j == 0 {
					_ = g.SetWeight(i, j, M)
				} else {
					_ = g.SetWeight(i, j, int64(j))
				}
			}
		}
		s := New()
		wf := func(i, j int) int64 { v, _ := g.Weight(i, j); return v }
		l, r := make([]int64, n), make([]int64, n)
		for i := range l {
			l[i] = M
		}
		mL, mR := make([]int, n), make([]int, n) // 零值即未匹配（km 内部 +1 约定）
		for i := 0; i < n-1; i++ {
			mL[i], mR[i] = i+1, i+1
		}
		augment(s, wf, n, n-1, l, r, mL, mR)
		if c := s.checks(); c != 1 {
			t.Fatalf("n=%d 单次求 delta 检查右节点=%d, want 1（全表扫描会随 n 线性增长）", n, c)
		}
		if mL[n-1] != n {
			t.Fatalf("n=%d 增广结果 mL[n-1]=%d, want %d", n, mL[n-1], n)
		}
	}
}
