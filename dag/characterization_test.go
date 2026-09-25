// 本文件是 characterization tests：钉住当前实现的真实行为（含非确定性与静默语义），
// 不表达「应当如此」的期望。所有用例表驱动，合并于此文件。
// 使用外部测试包 dag_test，以便同时导入 dag 与 crit（包内测试会形成 dag→crit→dag 导入环）。
package dag_test

import (
	"fmt"
	"sync"
	"testing"

	"ontology/crit"
	"ontology/dag"
)

type cn struct {
	name string
	lat  int64
}

func buildGraph(t *testing.T, ns []cn, es [][2]string) *dag.Graph {
	t.Helper()
	g := dag.New()
	for _, n := range ns {
		if err := g.Add(n.name, n.lat); err != nil {
			t.Fatalf("Add %s: %v", n.name, err)
		}
	}
	for _, e := range es {
		if err := g.Link(e[0], e[1]); err != nil {
			t.Fatalf("Link %v: %v", e, err)
		}
	}
	return g
}

func succOf(es [][2]string) map[string]map[string]bool {
	s := map[string]map[string]bool{}
	for _, e := range es {
		if s[e[0]] == nil {
			s[e[0]] = map[string]bool{}
		}
		s[e[0]][e[1]] = true
	}
	return s
}

func validTopo(topo []string, succ map[string]map[string]bool) bool {
	pos := map[string]int{}
	for i, n := range topo {
		pos[n] = i
	}
	for u, tos := range succ {
		for v := range tos {
			if pos[u] >= pos[v] {
				return false
			}
		}
	}
	return true
}

func validPath(path []string, source, sink string, succ map[string]map[string]bool) bool {
	if len(path) < 1 || path[0] != source || path[len(path)-1] != sink {
		return false
	}
	for i := 0; i+1 < len(path); i++ {
		if !succ[path[i]][path[i+1]] {
			return false
		}
	}
	return true
}

func pathLatSum(path []string, lat map[string]int64) int64 {
	var sum int64
	for _, n := range path {
		sum += lat[n]
	}
	return sum
}

func latMap(ns []cn) map[string]int64 {
	m := map[string]int64{}
	for _, n := range ns {
		m[n.name] = n.lat
	}
	return m
}

func outDegree(es [][2]string, from string) int {
	n := 0
	for _, e := range es {
		if e[0] == from {
			n++
		}
	}
	return n
}

const nondetRuns = 2000

// 边界 1：并行独立节点的 Topo 顺序随 map 迭代变化——同图反复 Solve 会得到多种合法拓扑序，
// 但 Source/Sink/EndToEnd 在所有观测中保持不变。
func TestSolveTopoNondeterminism(t *testing.T) {
	cases := []struct {
		name      string
		ns        []cn
		es        [][2]string
		source    string
		sink      string
		wantTopos map[string]bool
		e2e       int64
	}{
		{
			"two parallel branches",
			[]cn{{"S", 0}, {"A", 1}, {"B", 1}, {"T", 0}},
			[][2]string{{"S", "A"}, {"S", "B"}, {"A", "T"}, {"B", "T"}},
			"S", "T",
			map[string]bool{"[S A B T]": true, "[S B A T]": true},
			1,
		},
		{
			"three parallel branches",
			[]cn{{"S", 0}, {"a", 1}, {"b", 1}, {"c", 1}, {"T", 0}},
			[][2]string{{"S", "a"}, {"S", "b"}, {"S", "c"}, {"a", "T"}, {"b", "T"}, {"c", "T"}},
			"S", "T",
			map[string]bool{
				"[S a b c T]": true, "[S a c b T]": true, "[S b a c T]": true,
				"[S b c a T]": true, "[S c a b T]": true, "[S c b a T]": true,
			},
			1,
		},
		{
			"five-node mixed",
			[]cn{{"S", 0}, {"A", 30}, {"B", 50}, {"A2", 30}, {"T", 0}},
			[][2]string{{"S", "A"}, {"S", "B"}, {"A", "A2"}, {"B", "T"}, {"A2", "T"}},
			"S", "T",
			map[string]bool{"[S A B A2 T]": true, "[S B A A2 T]": true},
			60,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := buildGraph(t, tc.ns, tc.es)
			succ := succOf(tc.es)
			seen := map[string]bool{}
			for i := 0; i < nondetRuns; i++ {
				sol, err := g.Solve()
				if err != nil {
					t.Fatalf("Solve: %v", err)
				}
				if len(sol.Topo) != len(tc.ns) {
					t.Fatalf("topo len=%d want %d", len(sol.Topo), len(tc.ns))
				}
				key := fmt.Sprint(sol.Topo)
				if !tc.wantTopos[key] {
					t.Fatalf("unexpected topo %s", key)
				}
				if !validTopo(sol.Topo, succ) {
					t.Fatalf("invalid topo %v", sol.Topo)
				}
				if sol.EndToEnd != tc.e2e || sol.Source != tc.source || sol.Sink != tc.sink {
					t.Fatalf("got e2e=%d src=%s sink=%s, want %d %s %s",
						sol.EndToEnd, sol.Source, sol.Sink, tc.e2e, tc.source, tc.sink)
				}
				seen[key] = true
			}
			if len(seen) < 2 { // 当前真实行为：非确定（期望行为应是稳定单一序）
				t.Fatalf("topo appeared deterministic, only %v", seen)
			}
		})
	}
}

// 边界 2：等长关键路径并列时，严格 > 的松弛让「先被 map 迭代到的前驱」胜出，
// 同图反复 Analyze 的 Pred[sink] 与重建出的 Path 有多种结果。
func TestAnalyzeTiePathNondeterminism(t *testing.T) {
	cases := []struct {
		name      string
		ns        []cn
		es        [][2]string
		source    string
		sink      string
		e2e       int64
		wantPaths map[string]bool
	}{
		{
			"two equal paths z/a",
			[]cn{{"S", 0}, {"z", 40}, {"a", 40}, {"T", 0}},
			[][2]string{{"S", "z"}, {"S", "a"}, {"z", "T"}, {"a", "T"}},
			"S", "T", 40,
			map[string]bool{"[S z T]": true, "[S a T]": true},
		},
		{
			"all-zero diamond",
			[]cn{{"S", 0}, {"A", 0}, {"B", 0}, {"T", 0}},
			[][2]string{{"S", "A"}, {"S", "B"}, {"A", "T"}, {"B", "T"}},
			"S", "T", 0,
			map[string]bool{"[S A T]": true, "[S B T]": true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := buildGraph(t, tc.ns, tc.es)
			succ, lat := succOf(tc.es), latMap(tc.ns)
			seenPath, seenPredSink := map[string]bool{}, map[string]bool{}
			for i := 0; i < nondetRuns; i++ {
				sol, err := g.Solve()
				if err != nil {
					t.Fatalf("Solve: %v", err)
				}
				seenPredSink[sol.Pred[tc.sink]] = true
				r, err := crit.Analyze(g)
				if err != nil {
					t.Fatalf("Analyze: %v", err)
				}
				if r.EndToEnd != tc.e2e {
					t.Fatalf("EndToEnd=%d want %d", r.EndToEnd, tc.e2e)
				}
				key := fmt.Sprint(r.Path)
				if !tc.wantPaths[key] {
					t.Fatalf("unexpected path %s", key)
				}
				if !validPath(r.Path, tc.source, tc.sink, succ) {
					t.Fatalf("invalid path %v", r.Path)
				}
				if got := pathLatSum(r.Path, lat); got != tc.e2e {
					t.Fatalf("path lat=%d want EndToEnd=%d", got, tc.e2e)
				}
				seenPath[key] = true
			}
			if len(seenPath) < 2 { // 当前真实行为：Path 非确定
				t.Fatalf("Path appeared deterministic, only %v", seenPath)
			}
			if len(seenPredSink) < 2 { // 当前真实行为：并列前驱的 Pred 非确定
				t.Fatalf("Pred[%s] appeared deterministic, only %v", tc.sink, seenPredSink)
			}
		})
	}
}

// 边界 3：Bottleneck 在「任意关键路径」上全局选取（并列字典序最小），
// 而 Path 只是沿 Pred 回溯的某一条；多条关键路径节点集不同、最大 lat 节点只在其中一条时，
// 返回的 Path 可以不包含返回的 Bottleneck。
func TestBottleneckMayBeOffReturnedPath(t *testing.T) {
	cases := []struct {
		name   string
		ns     []cn
		es     [][2]string
		source string
		sink   string
		e2e    int64
		bn     string
		bnLat  int64
	}{
		{
			"bn only on upper path",
			[]cn{{"S", 0}, {"a", 10}, {"b", 9}, {"X", 6}, {"Y", 7}, {"T", 0}},
			[][2]string{{"S", "a"}, {"a", "X"}, {"X", "T"}, {"S", "b"}, {"b", "Y"}, {"Y", "T"}},
			"S", "T", 16, "a", 10,
		},
		{
			"bn only on upper path variant",
			[]cn{{"S", 0}, {"a", 10}, {"b", 9}, {"X", 5}, {"Y", 6}, {"T", 0}},
			[][2]string{{"S", "a"}, {"a", "X"}, {"X", "T"}, {"S", "b"}, {"b", "Y"}, {"Y", "T"}},
			"S", "T", 15, "a", 10,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := buildGraph(t, tc.ns, tc.es)
			succ, lat := succOf(tc.es), latMap(tc.ns)
			sawContaining, sawMissing := false, false
			for i := 0; i < nondetRuns; i++ {
				r, err := crit.Analyze(g)
				if err != nil {
					t.Fatalf("Analyze: %v", err)
				}
				if r.EndToEnd != tc.e2e || r.Bottleneck != tc.bn || r.BottleneckLat != tc.bnLat {
					t.Fatalf("got e2e=%d bn=(%s,%d), want %d (%s,%d)",
						r.EndToEnd, r.Bottleneck, r.BottleneckLat, tc.e2e, tc.bn, tc.bnLat)
				}
				if !r.OnCriticalPath[tc.bn] {
					t.Fatalf("Bottleneck %s not in OnCriticalPath", tc.bn)
				}
				if !validPath(r.Path, tc.source, tc.sink, succ) || pathLatSum(r.Path, lat) != tc.e2e {
					t.Fatalf("invalid critical path %v", r.Path)
				}
				onPath := false
				for _, n := range r.Path {
					if n == tc.bn {
						onPath = true
					}
				}
				if onPath {
					sawContaining = true
				} else {
					sawMissing = true // 当前真实行为：Path 可以不含 Bottleneck
				}
			}
			if !sawContaining || !sawMissing {
				t.Fatalf("want both path variants, containing=%v missing=%v", sawContaining, sawMissing)
			}
		})
	}
}

// 边界 4：重复 Link(A,B) 静默 no-op——第二次调用返回 nil、无错误提示，
// 邻接表不新增条目、EndToEnd 不变；其后成环边仍被正确拒绝。
func TestDuplicateLinkSilentNoop(t *testing.T) {
	cases := []struct {
		name       string
		ns         []cn
		es         [][2]string
		dup        [2]string
		e2e        int64
		cycleAfter [2]string
	}{
		{
			"single edge duplicated",
			[]cn{{"S", 0}, {"T", 0}}, [][2]string{{"S", "T"}},
			[2]string{"S", "T"}, 0,
			[2]string{"T", "S"},
		},
		{
			"diamond edge duplicated",
			[]cn{{"S", 0}, {"A", 1}, {"B", 1}, {"T", 0}},
			[][2]string{{"S", "A"}, {"S", "B"}, {"A", "T"}, {"B", "T"}},
			[2]string{"S", "A"}, 1,
			[2]string{"T", "S"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := buildGraph(t, tc.ns, tc.es)
			if err := g.Link(tc.dup[0], tc.dup[1]); err != nil { // 重复调用：真实行为是静默 nil
				t.Fatalf("duplicate Link returned err=%v, want nil", err)
			}
			if err := g.Link(tc.cycleAfter[0], tc.cycleAfter[1]); err == nil {
				t.Fatalf("cycle Link after duplicate unexpectedly succeeded")
			}
			sol, err := g.Solve()
			if err != nil {
				t.Fatalf("Solve: %v", err)
			}
			if sol.EndToEnd != tc.e2e {
				t.Fatalf("EndToEnd=%d want %d", sol.EndToEnd, tc.e2e)
			}
			_, snapSucc := g.Snapshot()
			wantOut := outDegree(tc.es, tc.dup[0])
			if len(snapSucc[tc.dup[0]]) != wantOut { // 邻接表未因重复调用多出条目
				t.Fatalf("duplicate Link changed adjacency of %s: %v (len=%d want %d)",
					tc.dup[0], snapSucc[tc.dup[0]], len(snapSucc[tc.dup[0]]), wantOut)
			}
		})
	}
}

// 边界 5（能测则测）：Analyze 先 Solve() 后 Snapshot()，是两次独立加锁读取。
// 在并发 Add/Link 增长下跑 Analyze 冒烟：当前实现互斥锁保证不会 panic / data race，
// 且成功返回时 Path 仍源→汇、沿边可达、长度和等于 EndToEnd。
// 两次快照之间可能跨图版本的非原子窗口极窄、无法稳定复现，只在 FINDINGS.md 说明。
func TestAnalyzeConcurrentMutationSmoke(t *testing.T) {
	cases := []struct {
		name string
		ns   []cn
		es   [][2]string
	}{
		{
			"growing diamond",
			[]cn{{"S", 0}, {"T", 0}},
			[][2]string{},
		},
	}
	const writers, readers, iters = 4, 8, 400
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := buildGraph(t, tc.ns, tc.es)
			stop := make(chan struct{})
			var wwg, rwg sync.WaitGroup
			var panicMu sync.Mutex
			panics := 0
			for w := 0; w < writers; w++ {
				wwg.Add(1)
				go func(w int) {
					defer wwg.Done()
					for k := 0; k < iters; k++ {
						name := fmt.Sprintf("w%d_k%d", w, k)
						if err := g.Add(name, int64(k%7)); err != nil {
							continue // 重名等拒绝：并发下的正常结果
						}
						_ = g.Link("S", name)
						_ = g.Link(name, "T")
					}
				}(w)
			}
			for r := 0; r < readers; r++ {
				rwg.Add(1)
				go func() {
					defer rwg.Done()
					defer func() {
						if recover() != nil {
							panicMu.Lock()
							panics++
							panicMu.Unlock()
						}
					}()
				loop:
					for {
						select {
						case <-stop:
							break loop
						default:
						}
						if res, err := crit.Analyze(g); err == nil {
							lat, succ := g.Snapshot()
							if !validPath(res.Path, "S", "T", succOf2(succ)) ||
								pathLatSum(res.Path, lat) != res.EndToEnd {
								t.Errorf("Analyze returned inconsistent result: path=%v e2e=%d",
									res.Path, res.EndToEnd)
							}
						}
					}
				}()
			}
			wwg.Wait()
			close(stop)
			rwg.Wait()
			if panics != 0 {
				t.Fatalf("concurrent Analyze panicked %d time(s)", panics)
			}
		})
	}
}

// succOf2 把 Snapshot 的邻接表转成测试用的集合形式。
func succOf2(succ map[string][]string) map[string]map[string]bool {
	m := map[string]map[string]bool{}
	for u, tos := range succ {
		m[u] = map[string]bool{}
		for _, v := range tos {
			m[u][v] = true
		}
	}
	return m
}
