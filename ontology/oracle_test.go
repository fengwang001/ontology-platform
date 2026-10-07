package ontology_test

import (
	"encoding/csv"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	ont "ontology/ontology"
)

// oracleLink 是朴素模型中的链接（与生产代码的 Link 无共享逻辑）。
type oracleLink struct {
	id            string
	bidirectional bool
	source        string
	target        string
}

// oracleWorld 是独立维护的一份世界状态，只通过公开 API 的操作序列同步。
type oracleWorld struct {
	objects  map[string]bool
	links    map[string]*oracleLink
	exist    map[string]map[string]bool
	traverse map[string]map[string]bool
}

func newOracleWorld(callers []string) *oracleWorld {
	w := &oracleWorld{
		objects:  map[string]bool{},
		links:    map[string]*oracleLink{},
		exist:    map[string]map[string]bool{},
		traverse: map[string]map[string]bool{},
	}
	for _, c := range callers {
		w.exist[c] = map[string]bool{}
		w.traverse[c] = map[string]bool{}
	}
	return w
}

// filterArcs 严格按题面次序：先存在性、后遍历性，构建去重有向弧。
func (w *oracleWorld) filterArcs(caller string) ([]string, map[string]map[string]bool, int, int) {
	visible := map[string]bool{}
	for o := range w.exist[caller] {
		if w.objects[o] {
			visible[o] = true
		}
	}
	vis := make([]string, 0, len(visible))
	for o := range visible {
		vis = append(vis, o)
	}
	sort.Strings(vis)

	arcs := map[string]map[string]bool{}
	candidate, used := 0, 0
	for _, l := range w.links {
		if !visible[l.source] || !visible[l.target] {
			continue
		}
		candidate++
		if !w.traverse[caller][l.id] {
			continue
		}
		used++
		add := func(a, b string) {
			if arcs[a] == nil {
				arcs[a] = map[string]bool{}
			}
			arcs[a][b] = true
		}
		add(l.source, l.target)
		if l.bidirectional {
			add(l.target, l.source)
		}
	}
	_ = used
	return vis, arcs, len(vis), candidate
}

// hasCycleKahn 用 Kahn 拓扑排序做朴素环判定。
func hasCycleKahn(vis []string, arcs map[string]map[string]bool) bool {
	indeg := map[string]int{}
	for _, v := range vis {
		for to := range arcs[v] {
			indeg[to]++
		}
	}
	var queue []string
	for _, v := range vis {
		if indeg[v] == 0 {
			queue = append(queue, v)
		}
	}
	removed := 0
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		removed++
		for v := range arcs[u] {
			indeg[v]--
			if indeg[v] == 0 {
				queue = append(queue, v)
			}
		}
	}
	return removed < len(vis)
}

// hasCycleDFS 用染色 DFS 做另一种朴素环判定（与 Kahn 算法独立）。
func hasCycleDFS(vis []string, arcs map[string]map[string]bool) bool {
	const white, gray, black = 0, 1, 2
	color := map[string]int{}
	var visit func(u string) bool
	visit = func(u string) bool {
		color[u] = gray
		neighbors := make([]string, 0, len(arcs[u]))
		for v := range arcs[u] {
			neighbors = append(neighbors, v)
		}
		sort.Strings(neighbors)
		for _, v := range neighbors {
			if color[v] == gray {
				return true
			}
			if color[v] == white && visit(v) {
				return true
			}
		}
		color[u] = black
		return false
	}
	for _, s := range vis {
		if color[s] == white && visit(s) {
			return true
		}
	}
	return false
}

func buildRandomishGraph(t *testing.T, n, m int) *ont.Graph {
	t.Helper()
	rng := rand.New(rand.NewSource(int64(n*1000 + m)))
	g := newTestGraph(t)
	for i := 0; i < n; i++ {
		must(t, g.AddObject(fmt.Sprintf("o%d", i), "T"))
	}
	for i := 0; i < m; i++ {
		a, b := rng.Intn(n), rng.Intn(n)
		tid := "D"
		if rng.Intn(2) == 1 {
			tid = "B"
		}
		id := fmt.Sprintf("r%d", i)
		err := g.AddLink(&ont.Link{
			ID:     id,
			Type:   linkType(g, tid),
			Source: fmt.Sprintf("o%d", a),
			Target: fmt.Sprintf("o%d", b),
		})
		if err != nil {
			continue
		}
		if rng.Intn(2) == 1 {
			_ = g.GrantExist("alice", fmt.Sprintf("o%d", a))
			_ = g.GrantExist("alice", fmt.Sprintf("o%d", b))
			_ = g.GrantTraverse("alice", id)
		}
	}
	return g
}

// TestRandomDifferential 在随机对象、链接、方向与权限分配序列上，将
// HasCycle 与两个独立朴素模型（Kahn、染色 DFS）对照；同时校验证据
// 真实成环、多次调用证据稳定，并记录每次调用的输入/输出/证据。
func TestRandomDifferential(t *testing.T) {
	must(t, os.MkdirAll("testdata", 0o755))
	f, err := os.Create(filepath.Join("testdata", "has_cycle_calls.csv"))
	must(t, err)
	defer f.Close()
	cw := csv.NewWriter(f)
	must(t, cw.Write([]string{
		"trial", "call_index", "caller", "visible_objects",
		"candidate_links", "has_cycle", "evidence",
	}))

	const trials = 400
	for trial := 0; trial < trials; trial++ {
		rng := rand.New(rand.NewSource(int64(trial) + 1))
		g := newTestGraph(t)
		w := newOracleWorld([]string{"alice", "bob"})

		n := 1 + rng.Intn(8)
		objIDs := make([]string, n)
		for i := 0; i < n; i++ {
			objIDs[i] = fmt.Sprintf("o%d", i)
			must(t, g.AddObject(objIDs[i], "T"))
			w.objects[objIDs[i]] = true
		}

		// 记录已创建链接，供后续随机翻转权限/删除。
		var linkIDs []string
		linkMeta := map[string]*oracleLink{}

		callIndex := 0
		recordCall := func(caller string, visCount, cand int, res ont.CycleResult) {
			must(t, cw.Write([]string{
				fmt.Sprintf("%d", trial),
				fmt.Sprintf("%d", callIndex),
				caller,
				fmt.Sprintf("%d", visCount),
				fmt.Sprintf("%d", cand),
				fmt.Sprintf("%v", res.HasCycle),
				strings.Join(res.Cycle, "->"),
			}))
			callIndex++
		}

		mutations := 2 * n
		for step := 0; step < mutations; step++ {
			caller := []string{"alice", "bob"}[rng.Intn(2)]
			switch rng.Intn(4) {
			case 0, 1: // 增加链接
				a, b := objIDs[rng.Intn(n)], objIDs[rng.Intn(n)]
				bidir := rng.Intn(2) == 1
				tid := "D"
				if bidir {
					tid = "B"
				}
				id := fmt.Sprintf("l%d_%d", trial, step)
				err := g.AddLink(&ont.Link{ID: id, Type: linkType(g, tid), Source: a, Target: b})
				if err == nil {
					ol := &oracleLink{id: id, bidirectional: bidir, source: a, target: b}
					w.links[id] = ol
					linkIDs = append(linkIDs, id)
					linkMeta[id] = ol
					if rng.Intn(2) == 1 {
						must(t, g.GrantExist(caller, a))
						must(t, g.GrantExist(caller, b))
						must(t, g.GrantTraverse(caller, id))
						w.exist[caller][a] = true
						w.exist[caller][b] = true
						w.traverse[caller][id] = true
					}
				}
			case 2: // 翻转遍历权限
				if len(linkIDs) > 0 {
					id := linkIDs[rng.Intn(len(linkIDs))]
					if w.traverse[caller][id] {
						must(t, g.RevokeTraverse(caller, id))
						delete(w.traverse[caller], id)
					} else {
						ol := linkMeta[id]
						must(t, g.GrantExist(caller, ol.source))
						must(t, g.GrantExist(caller, ol.target))
						must(t, g.GrantTraverse(caller, id))
						w.exist[caller][ol.source] = true
						w.exist[caller][ol.target] = true
						w.traverse[caller][id] = true
					}
				}
			case 3: // 翻转存在性权限
				id := objIDs[rng.Intn(n)]
				if w.exist[caller][id] {
					must(t, g.RevokeExist(caller, id))
					delete(w.exist[caller], id)
				} else {
					must(t, g.GrantExist(caller, id))
					w.exist[caller][id] = true
				}
			}

			// 每个变更点对两个调用者分别判定并对照。
			for _, caller := range []string{"alice", "bob"} {
				res, err := g.HasCycle(caller)
				must(t, err)
				again, err := g.HasCycle(caller)
				must(t, err)
				if fmt.Sprint(res.Cycle) != fmt.Sprint(again.Cycle) || res.HasCycle != again.HasCycle {
					t.Fatalf("trial %d: repeated calls disagree: %+v vs %+v", trial, res, again)
				}
				vis, arcs, visCount, cand := w.filterArcs(caller)
				if res.VisitedObjects != visCount || res.VisitedLinks != cand {
					t.Fatalf("trial %d: metric mismatch got=(%d,%d) want=(%d,%d)",
						trial, res.VisitedObjects, res.VisitedLinks, visCount, cand)
				}
				kahn := hasCycleKahn(vis, arcs)
				dfs := hasCycleDFS(vis, arcs)
				if kahn != dfs || kahn != res.HasCycle {
					t.Fatalf("trial %d caller %s: impl=%v kahn=%v dfs=%v",
						trial, caller, res.HasCycle, kahn, dfs)
				}
				if res.HasCycle {
					// 证据必须由当前可见弧支撑且为最小简单环：
					// 每个顶点在环中恰有一条出弧闭合，且不存在更短环——
					// 这里校验证据本身闭合、无重复，且长度等于 oracle
					// 用 BFS 独立求出的最短环长度。
					if !arcsSupport(arcs, res.Cycle) {
						t.Fatalf("trial %d: evidence not a real cycle: %v", trial, res.Cycle)
					}
					shortest := oracleShortestCycle(vis, arcs)
					if len(res.Cycle) != shortest {
						t.Fatalf("trial %d: evidence length %d != shortest %d (%v)",
							trial, len(res.Cycle), shortest, res.Cycle)
					}
				}
				recordCall(caller, visCount, cand, res)
			}
		}
	}
	cw.Flush()
	must(t, cw.Error())
}

func arcsSupport(arcs map[string]map[string]bool, cycle []string) bool {
	seen := map[string]bool{}
	for _, id := range cycle {
		if seen[id] {
			return false
		}
		seen[id] = true
	}
	for i := range cycle {
		if !arcs[cycle[i]][cycle[(i+1)%len(cycle)]] {
			return false
		}
	}
	return true
}

// oracleShortestCycle 以每个可见顶点为根独立 BFS 求最短有向环长度，
// 与生产实现的写法刻意不同（不重建父路径，只返回长度）。
func oracleShortestCycle(vis []string, arcs map[string]map[string]bool) int {
	best := 0
	for _, root := range vis {
		dist := map[string]int{root: 0}
		queue := []string{root}
		found := -1
		for len(queue) > 0 && found == -1 {
			u := queue[0]
			queue = queue[1:]
			nbrs := make([]string, 0, len(arcs[u]))
			for v := range arcs[u] {
				nbrs = append(nbrs, v)
			}
			sort.Strings(nbrs)
			for _, v := range nbrs {
				if v == root {
					found = dist[u] + 1
					break
				}
				if _, ok := dist[v]; !ok {
					dist[v] = dist[u] + 1
					queue = append(queue, v)
				}
			}
		}
		if found > 0 && (best == 0 || found < best) {
			best = found
		}
	}
	return best
}
