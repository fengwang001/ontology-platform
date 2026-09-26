package api

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"ontology/dg"
)

type prng struct{ s uint64 } // 确定性 xorshift，生成随机加边顺序

func (p *prng) next() uint64 {
	p.s ^= p.s << 13
	p.s ^= p.s >> 7
	p.s ^= p.s << 17
	return p.s
}

// genGraph 生成 n 个节点、m 条唯一边的图，加边顺序由 seed 决定的随机序。
func genGraph(n, m int, seed uint64) graphSpec {
	if max := n * (n - 1); m > max { // 唯一有向边（无自环）最多 n*(n-1) 条
		m = max
	}
	p, seen := &prng{s: seed*2654435761 + 1}, map[[2]int]bool{}
	s := graphSpec{n: n}
	for len(s.edges) < m {
		u, v := int(p.next()%uint64(n)), int(p.next()%uint64(n))
		if u != v && !seen[[2]int{u, v}] {
			seen[[2]int{u, v}] = true
			s.edges = append(s.edges, [2]int{u, v})
		}
	}
	return s
}

func mustBuild(t *testing.T, s graphSpec) *API {
	a, err := build(s)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// 不变量 1：Reach(i,j) 为 true 当且仅当存在 ≥1 条边的路径（含对角线语义）。
func TestDefinitionEdgeCases(t *testing.T) {
	cy := mustBuild(t, graphSpec{5, [][2]int{{0, 1}, {1, 2}, {2, 0}, {2, 3}, {3, 4}}})
	ij := [][2]int{{0, 4}, {4, 0}, {3, 4}, {0, 0}, {2, 2}, {3, 3}, {4, 4}}
	// 后四对是对角线：0、2 在环上自可达，3、4 不在环上自不可达。
	want := []bool{true, false, true, true, true, false, false}
	for k := range ij {
		if got := cy.Reach(ij[k][0], ij[k][1]); got != want[k] {
			t.Errorf("Reach%v=%v, want %v", ij[k], got, want[k])
		}
	}
}

// 不变量 2：闭包矩阵与逐点 DFS 朴素参照逐 (i,j) 相同。
func TestClosureMatchesNaive(t *testing.T) {
	for _, n := range []int{2, 3, 5, 8, 13, 21} {
		for seed := uint64(1); seed <= 3; seed++ {
			s := genGraph(n, 2*n, seed)
			a, naive := mustBuild(t, s), naiveClosure(s)
			for i := 0; i < n; i++ {
				for j := 0; j < n; j++ {
					if a.Reach(i, j) != naive[i][j] {
						t.Fatalf("n=%d seed=%d (%d,%d): api=%v naive=%v", n, seed, i, j, a.Reach(i, j), naive[i][j])
					}
				}
			}
		}
	}
}

// 不变量 3：闭包是传递的。
func TestTransitivity(t *testing.T) {
	for _, n := range []int{3, 7, 16} {
		a := mustBuild(t, genGraph(n, 2*n, uint64(n)))
		for i := 0; i < n; i++ {
			for k := 0; k < n; k++ {
				for j := 0; j < n; j++ {
					if a.Reach(i, k) && a.Reach(k, j) && !a.Reach(i, j) {
						t.Fatalf("n=%d: Reach(%d,%d)&&Reach(%d,%d) but !Reach(%d,%d)", n, i, k, k, j, i, j)
					}
				}
			}
		}
	}
}

// 不变量 4：四类可判定错误互不相同，被拒后状态不变且仍可正常使用。
func TestErrorsLeaveNoTrace(t *testing.T) {
	for _, n := range []int{0, -1, -100} {
		if _, err := New(n); !errors.Is(err, dg.ErrNonPositiveN) {
			t.Fatalf("New(%d) err=%v, want ErrNonPositiveN", n, err)
		}
	}
	a := mustBuild(t, graphSpec{4, [][2]int{{0, 1}}})
	uv := [][2]int{{0, 4}, {-1, 0}, {0, -1}, {2, 2}, {0, 1}}
	want := []error{dg.ErrNodeOutOfRange, dg.ErrNodeOutOfRange, dg.ErrNodeOutOfRange, dg.ErrSelfLoop, dg.ErrDuplicateEdge}
	seen := map[error]bool{dg.ErrNonPositiveN: true}
	for k := range uv {
		if err := a.AddEdge(uv[k][0], uv[k][1]); !errors.Is(err, want[k]) || a.EdgeCount() != 1 {
			t.Fatalf("AddEdge(%d,%d) err=%v EdgeCount=%d", uv[k][0], uv[k][1], err, a.EdgeCount())
		}
		seen[want[k]] = true
	}
	if len(seen) != 4 {
		t.Fatalf("sentinel errors not distinct: %d", len(seen))
	}
	if err := a.AddEdge(1, 2); err != nil || a.EdgeCount() != 2 { // 被拒后仍可正常使用
		t.Fatalf("reuse after rejections: err=%v EdgeCount=%d", err, a.EdgeCount())
	}
	if err := SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// 并发：N 个 goroutine 对同一批 (i,j) 查询 Reach 结果逐对相同，不用 sleep 制造时序。
func TestConcurrentReach(t *testing.T) {
	a := mustBuild(t, genGraph(50, 200, 7))
	var pairs [][2]int
	for k := 0; k < 200; k++ {
		pairs = append(pairs, [2]int{(k * 7) % 50, (k * 13) % 50})
	}
	results := make([][]bool, 16)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			results[g] = make([]bool, len(pairs))
			for k, p := range pairs {
				results[g][k] = a.Reach(p[0], p[1])
			}
			_ = a.EdgeCount()
			_ = SelfCheck() // 并发调用读方法，-race 下验证安全
		}(g)
	}
	close(start)
	wg.Wait()
	for g := 1; g < 16; g++ {
		if !slices.Equal(results[g], results[0]) {
			t.Fatalf("goroutine %d results differ from goroutine 0", g)
		}
	}
}
