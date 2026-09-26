package tc

import (
	"math/rand"
	"sync"
	"testing"

	"ontology/dg"
)

func bfsRef(n int, edges [][2]int) [][]bool {
	adj := make([][]int, n)
	for _, e := range edges {
		adj[e[0]] = append(adj[e[0]], e[1])
	}
	want := make([][]bool, n)
	for s := 0; s < n; s++ {
		want[s] = make([]bool, n)
		seen := make([]bool, n) // s 不预标记：空路径不算可达，回边到 s 才置自可达
		q := []int{s}
		for len(q) > 0 {
			u := q[0]
			q = q[1:]
			for _, v := range adj[u] {
				if !seen[v] {
					seen[v] = true
					want[s][v] = true
					q = append(q, v)
				}
			}
		}
	}
	return want
}
func buildTC(n int, edges [][2]int) *TC {
	g := dg.New(n)
	for _, e := range edges {
		_ = g.AddEdge(e[0], e[1])
	}
	t := New(g)
	t.Compute()
	return t
}
func randEdges(r *rand.Rand, n, d int) [][2]int {
	var e [][2]int
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i != j && r.Intn(d) == 0 {
				e = append(e, [2]int{i, j})
			}
		}
	}
	return e
}
func checkAll(t *testing.T, tc *TC, n int, want func(int, int) bool) {
	for q := 0; q < n*n; q++ {
		if tc.Reach(q/n, q%n) != want(q/n, q%n) {
			t.Fatalf("(%d,%d)", q/n, q%n)
		}
	}
}
func TestSpecGraphClosure(t *testing.T) {
	for _, c := range []struct {
		n    int
		e    [][2]int
		rows []string
	}{
		{5, [][2]int{{0, 1}, {1, 2}, {2, 0}, {2, 3}, {3, 4}}, []string{"11111", "11111", "11111", "00001", "00000"}},
		{1, nil, []string{"0"}},
		{4, [][2]int{{0, 1}, {1, 0}, {2, 3}}, []string{"1100", "1100", "0001", "0000"}},
	} {
		tc := buildTC(c.n, c.e)
		checkAll(t, tc, c.n, func(i, j int) bool { return c.rows[i][j] == '1' })
	}
}
func TestNaiveReference(t *testing.T) {
	spec := [][2]int{{0, 1}, {1, 2}, {2, 0}, {2, 3}, {3, 4}}
	ref := bfsRef(5, spec)
	checkAll(t, buildTC(5, spec), 5, func(i, j int) bool { return ref[i][j] })
	for seed := int64(0); seed < 8; seed++ {
		r := rand.New(rand.NewSource(seed))
		n := 1 + r.Intn(25)
		e := randEdges(r, n, 5)
		sh := append([][2]int(nil), e...)
		r.Shuffle(len(sh), func(i, j int) { sh[i], sh[j] = sh[j], sh[i] })
		t1, t2 := buildTC(n, e), buildTC(n, sh)
		want := bfsRef(n, e)
		checkAll(t, t1, n, func(i, j int) bool { return want[i][j] })
		checkAll(t, t2, n, t1.Reach)
	}
}
func TestTransitivity(t *testing.T) {
	for seed := int64(0); seed < 6; seed++ {
		r := rand.New(rand.NewSource(seed + 50))
		n := 1 + r.Intn(12)
		tc := buildTC(n, randEdges(r, n, 4))
		for q := 0; q < n*n*n; q++ {
			i, k, j := q/(n*n), (q/n)%n, q%n
			if tc.Reach(i, k) && tc.Reach(k, j) && !tc.Reach(i, j) {
				t.Fatalf("(%d,%d,%d)", i, k, j)
			}
		}
	}
}
func TestQueryCounterConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		e := make([][2]int, 0, m)
		for i := 1; i < m; i++ {
			e = append(e, [2]int{i - 1, i})
		}
		tc := buildTC(m, e)
		for q := 0; q < 100; q++ {
			i, j := q%m, (q*7+3)%m
			if tc.Reach(i, j) != (i < j) || tc.lastChecks.Load() != 1 {
				t.Fatalf("m=%d q=%d not constant", m, q)
			}
		}
	}
}
func TestConcurrentReads(t *testing.T) {
	e := make([][2]int, 20)
	for i := range e {
		e[i] = [2]int{i, (i + 1) % 20}
	}
	tc := buildTC(20, e)
	const g, p = 16, 400
	res := make([][p]bool, g)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for x := 0; x < g; x++ {
		wg.Add(1)
		go func(x int) {
			defer wg.Done()
			<-start
			for q := 0; q < p; q++ {
				res[x][q] = tc.Reach(q/20, q%20)
			}
		}(x)
	}
	close(start)
	wg.Wait()
	for x := 1; x < g; x++ {
		if res[x] != res[0] {
			t.Fatalf("goroutine %d disagrees", x)
		}
	}
}
