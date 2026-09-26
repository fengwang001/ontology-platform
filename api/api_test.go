package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"testing"

	"ontology/api"
)

func mustBuild(t *testing.T, n int, edges [][3]int64) *api.API {
	a, err := api.New(n)
	for _, e := range edges {
		if err == nil {
			err = a.AddEdge(int(e[0]), int(e[1]), e[2])
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// 固定用例表：第三节图、多源、零权、负权、并列字典序、负dist新起、中途不得重启。
func TestSolveTable(t *testing.T) {
	sec3 := [][3]int64{{0, 1, 2}, {0, 2, 4}, {1, 3, 5}, {2, 3, 3}, {3, 4, 1}, {2, 4, 7}, {1, 4, 9}}
	cases := []struct {
		name     string
		n, total int
		edges    [][3]int64
		path     []int
	}{
		{"第三节", 5, 11, sec3, []int{0, 1, 4}},
		{"第三节加新源", 7, 100, append(slices.Clone(sec3), [3]int64{5, 6, 100}), []int{5, 6}},
		{"全负取最不负", 2, -5, [][3]int64{{0, 1, -5}}, []int{0, 1}},
		{"零权", 3, 0, [][3]int64{{0, 1, 0}, {1, 2, 0}}, []int{0, 1}},
		{"并列取字典序最小", 4, 2, [][3]int64{{0, 1, 1}, {1, 2, 1}, {0, 2, 2}}, []int{0, 1, 2}},
		{"深源胜出", 6, 7, [][3]int64{{3, 4, 7}, {4, 5, -2}, {0, 1, 1}}, []int{3, 4}},
		{"负dist新起路径", 3, 8, [][3]int64{{2, 0, -3}, {0, 1, 8}, {2, 1, 7}}, []int{0, 1}},
		{"中途不得重启", 3, 6, [][3]int64{{1, 2, -3}, {1, 0, -1}, {2, 0, 6}}, []int{2, 0}},
	}
	for _, c := range cases {
		total, path, err := mustBuild(t, c.n, c.edges).Solve()
		if err != nil || total != int64(c.total) || !slices.Equal(path, c.path) {
			t.Fatalf("%s: got %d %v %v, want %d %v", c.name, total, path, err, c.total, c.path)
		}
	}
}

// 六类哨兵错误可判定且互不相同；每次被拒后状态不变、图仍可用（不变量4）。
func TestErrorsDistinctAndStateKept(t *testing.T) {
	a := mustBuild(t, 3, [][3]int64{{0, 1, 5}})
	_, _, errCyc := mustBuild(t, 2, [][3]int64{{0, 1, 1}, {1, 0, 1}}).Solve()
	_, _, errEmpty := mustBuild(t, 2, nil).Solve()
	_, e0 := api.New(0)
	bads := []error{e0, a.AddEdge(0, 3, 1), a.AddEdge(1, 1, 1), a.AddEdge(0, 1, 9), errCyc, errEmpty}
	wants := []error{api.ErrBadOrder, api.ErrNodeOutOfRange, api.ErrSelfLoop, api.ErrDuplicateEdge, api.ErrCycle, api.ErrNoPath}
	for i, got := range bads {
		for j, want := range wants {
			if errors.Is(got, want) != (i == j) {
				t.Fatalf("bads[%d]=%v wants[%d] mismatch", i, got, j)
			}
		}
	}
	total, path, err := a.Solve()
	if a.EdgeCount() != 1 || err != nil || total != 5 || !slices.Equal(path, []int{0, 1}) {
		t.Fatalf("state after rejections: %d %d %v %v", a.EdgeCount(), total, path, err)
	}
}

// 与朴素参照逐值一致（不变量1/2/3）：多档规模 × 多种子 × 随机加边顺序。
func TestBruteForce(t *testing.T) {
	for _, n := range []int{2, 3, 4, 5, 6, 7, 8} {
		for seed := int64(0); seed < 20; seed++ {
			edges := randDAG(n, rand.New(rand.NewSource(seed*100+int64(n))))
			total, path, err := mustBuild(t, n, edges).Solve()
			want, wantPath, ok := brute(n, edges)
			if !ok {
				if !errors.Is(err, api.ErrNoPath) {
					t.Fatalf("n=%d seed=%d: want ErrNoPath, got %v", n, seed, err)
				}
				continue
			}
			if err != nil || total != want || !slices.Equal(path, wantPath) {
				t.Fatalf("n=%d seed=%d: got %d %v, want %d %v", n, seed, total, path, want, wantPath)
			}
		}
	}
}

func randDAG(n int, rng *rand.Rand) [][3]int64 {
	perm := rng.Perm(n)
	var cand [][3]int64
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			cand = append(cand, [3]int64{int64(perm[i]), int64(perm[j]), int64(rng.Intn(21) - 10)})
		}
	}
	rng.Shuffle(len(cand), func(i, j int) { cand[i], cand[j] = cand[j], cand[i] })
	return cand[:rng.Intn(len(cand)+1)]
}

func brute(n int, edges [][3]int64) (int64, []int, bool) {
	adj := make([][][2]int64, n)
	for _, e := range edges {
		adj[e[0]] = append(adj[e[0]], [2]int64{e[1], e[2]})
	}
	best := int64(-1) << 62
	var bestPath []int
	var dfs func(u int, sum int64, cur []int)
	dfs = func(u int, sum int64, cur []int) {
		if len(cur) > 1 && (sum > best || (sum == best && slices.Compare(cur, bestPath) < 0)) {
			best, bestPath = sum, slices.Clone(cur)
		}
		for _, e := range adj[u] {
			dfs(int(e[0]), sum+e[1], append(cur, int(e[0])))
		}
	}
	for v := 0; v < n; v++ {
		dfs(v, 0, []int{v})
	}
	return best, bestPath, len(bestPath) > 0
}

// 并发：N 个 goroutine 对同一 DAG 并发 Solve/EdgeCount/SelfCheck，结果逐元素相同（不用 sleep）。
func TestConcurrentSolve(t *testing.T) {
	a := mustBuild(t, 5, [][3]int64{{0, 1, 2}, {0, 2, 4}, {1, 3, 5}, {2, 3, 3}, {3, 4, 1}, {2, 4, 7}, {1, 4, 9}})
	start, out := make(chan struct{}), make(chan string, 16)
	for i := 0; i < 16; i++ {
		go func() {
			<-start
			total, path, err := a.Solve()
			out <- fmt.Sprint(total, path, err, a.EdgeCount(), a.SelfCheck())
		}()
	}
	close(start)
	for i := 0; i < 16; i++ {
		if got := <-out; got != "11 [0 1 4] <nil> 7 <nil>" {
			t.Fatalf("inconsistent result: %s", got)
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if err := mustBuild(t, 2, [][3]int64{{0, 1, 1}}).SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
