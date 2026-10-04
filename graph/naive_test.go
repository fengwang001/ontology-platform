package graph

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// naive is a reference implementation that recomputes bridges from
// scratch with a DFS low-point pass after every accepted insertion.
type naive struct {
	n  int
	us []int // 1-based edge table
	vs []int
	ws []int

	added []int // version at insertion
	free  []int // first version the edge was seen as a non-bridge

	isBridge []bool
	blockOf  []int // block label per node
	compOf   []int // component root per node
	bWeight  map[int]int64
}

func newNaive(n int) *naive {
	ng := &naive{
		n:        n,
		us:       []int{-1},
		vs:       []int{-1},
		ws:       []int{-1},
		added:    []int{-1},
		free:     []int{-1},
		isBridge: make([]bool, 1),
		blockOf:  make([]int, n),
		compOf:   make([]int, n),
		bWeight:  map[int]int64{},
	}
	for i := 0; i < n; i++ {
		ng.blockOf[i] = i
		ng.compOf[i] = i
		ng.bWeight[i] = 0
	}
	return ng
}

// kind classifies an insertion against the current (pre-insertion)
// derived state.
func (ng *naive) kind(u, v int) Kind {
	if ng.compOf[u] != ng.compOf[v] {
		return KindLink
	}
	if ng.blockOf[u] == ng.blockOf[v] {
		return KindInside
	}
	return KindMerge
}

// add records the edge and recomputes every derived fact. It returns
// the old labels of the merged blocks and the ids of the edges that
// stopped being bridges (both only meaningful for a Merge).
func (ng *naive) add(u, v, w, ver int) (merged []int, unbridged []int) {
	k := ng.kind(u, v)
	oldBlock := make([]int, ng.n)
	copy(oldBlock, ng.blockOf)
	wasBridge := make([]bool, len(ng.isBridge))
	copy(wasBridge, ng.isBridge)

	ng.us = append(ng.us, u)
	ng.vs = append(ng.vs, v)
	ng.ws = append(ng.ws, w)
	ng.added = append(ng.added, ver)
	ng.free = append(ng.free, 0)
	ng.recompute(ver)

	id := len(ng.us) - 1
	for e := 1; e < id; e++ {
		if wasBridge[e] && !ng.isBridge[e] {
			unbridged = append(unbridged, e)
		}
	}
	if k == KindMerge {
		seen := map[int]bool{}
		newBlock := ng.blockOf[u]
		for x := 0; x < ng.n; x++ {
			if ng.blockOf[x] == newBlock && !seen[oldBlock[x]] {
				seen[oldBlock[x]] = true
				merged = append(merged, oldBlock[x])
			}
		}
		sort.Ints(merged)
	}
	return merged, unbridged
}

// recompute derives bridges, blocks, components and block weights from
// scratch and folds them into the per-edge history.
func (ng *naive) recompute(ver int) {
	m := len(ng.us) - 1
	adj := make([][]int, ng.n)
	for e := 1; e <= m; e++ {
		adj[ng.us[e]] = append(adj[ng.us[e]], e)
		adj[ng.vs[e]] = append(adj[ng.vs[e]], e)
	}

	// Iterative Tarjan low-point pass (edge-id aware, multigraph safe).
	ng.isBridge = make([]bool, m+1)
	tin := make([]int, ng.n)
	low := make([]int, ng.n)
	visited := make([]bool, ng.n)
	timer := 0
	type frame struct{ v, pe, idx int }
	for s := 0; s < ng.n; s++ {
		if visited[s] {
			continue
		}
		timer++
		tin[s], low[s] = timer, timer
		visited[s] = true
		stack := []frame{{v: s, pe: -1}}
		for len(stack) > 0 {
			f := &stack[len(stack)-1]
			if f.idx < len(adj[f.v]) {
				e := adj[f.v][f.idx]
				f.idx++
				if e == f.pe {
					continue
				}
				to := ng.us[e] + ng.vs[e] - f.v
				if visited[to] {
					if tin[to] < low[f.v] {
						low[f.v] = tin[to]
					}
					continue
				}
				visited[to] = true
				timer++
				tin[to], low[to] = timer, timer
				stack = append(stack, frame{v: to, pe: e})
			} else {
				stack = stack[:len(stack)-1]
				if len(stack) == 0 {
					break
				}
				p := &stack[len(stack)-1]
				if low[f.v] < low[p.v] {
					low[p.v] = low[f.v]
				}
				if low[f.v] > tin[p.v] {
					ng.isBridge[f.pe] = true
				}
			}
		}
	}

	// Blocks: DSU over non-bridge edges; components: DSU over all edges.
	pb := make([]int, ng.n)
	pc := make([]int, ng.n)
	for i := range pb {
		pb[i], pc[i] = i, i
	}
	var find func(p []int, x int) int
	find = func(p []int, x int) int {
		for p[x] != x {
			p[x] = p[p[x]]
			x = p[x]
		}
		return x
	}
	for e := 1; e <= m; e++ {
		ru, rv := find(pc, ng.us[e]), find(pc, ng.vs[e])
		if ru != rv {
			pc[ru] = rv
		}
		if ng.isBridge[e] {
			continue
		}
		ru, rv = find(pb, ng.us[e]), find(pb, ng.vs[e])
		if ru != rv {
			pb[ru] = rv
		}
	}
	for x := 0; x < ng.n; x++ {
		ng.compOf[x] = find(pc, x)
	}
	// Block label = smallest node id in the block.
	label := map[int]int{}
	for x := 0; x < ng.n; x++ {
		r := find(pb, x)
		if l, ok := label[r]; !ok || x < l {
			label[r] = x
		}
	}
	for x := 0; x < ng.n; x++ {
		ng.blockOf[x] = label[find(pb, x)]
	}
	ng.bWeight = map[int]int64{}
	for e := 1; e <= m; e++ {
		if ng.isBridge[e] {
			continue
		}
		ng.bWeight[ng.blockOf[ng.us[e]]] += int64(ng.ws[e])
	}
	for e := 1; e <= m; e++ {
		if ng.free[e] == 0 && !ng.isBridge[e] {
			ng.free[e] = ver
		}
	}
}

// bridgesOnPath lists every bridge whose removal disconnects u from v.
func (ng *naive) bridgesOnPath(u, v int) []int {
	m := len(ng.us) - 1
	var ids []int
	for e := 1; e <= m; e++ {
		if !ng.isBridge[e] {
			continue
		}
		// BFS from u avoiding edge e.
		seen := make([]bool, ng.n)
		queue := []int{u}
		seen[u] = true
		for len(queue) > 0 {
			x := queue[0]
			queue = queue[1:]
			for f := 1; f <= m; f++ {
				if f == e {
					continue
				}
				var to int
				switch ng.us[f] {
				case x:
					to = ng.vs[f]
				default:
					if ng.vs[f] == x {
						to = ng.us[f]
					} else {
						continue
					}
				}
				if !seen[to] {
					seen[to] = true
					queue = append(queue, to)
				}
			}
		}
		if !seen[v] {
			ids = append(ids, e)
		}
	}
	sort.Ints(ids)
	return ids
}

// TestNaiveComparison replays 2000 random insertion sequences against
// the from-scratch low-point reference and compares every reportable
// fact after every single insertion.
func TestNaiveComparison(t *testing.T) {
	const sequences = 2000
	rng := rand.New(rand.NewSource(1195))
	for seq := 0; seq < sequences; seq++ {
		n := 2 + rng.Intn(13)   // 2..14 nodes
		ops := 5 + rng.Intn(56) // 5..60 insertions
		type op struct{ u, v, w int }
		script := make([]op, 0, ops)
		for len(script) < ops {
			u := rng.Intn(n)
			v := rng.Intn(n)
			if u == v {
				continue
			}
			script = append(script, op{u, v, 1 + rng.Intn(8)})
		}

		m, err := New(n, ops)
		if err != nil {
			t.Fatalf("seq %d: New: %v", seq, err)
		}
		ng := newNaive(n)

		fail := func(format string, args ...interface{}) {
			t.Logf("seq %d input: n=%d ops=%v", seq, n, script)
			t.Fatalf("seq %d: "+format, append([]interface{}{seq}, args...)...)
		}

		for i, o := range script {
			ver := i + 1
			wantKind := ng.kind(o.u, o.v)
			res, err := m.AddEdge(o.u, o.v, o.w)
			if err != nil {
				fail("AddEdge(%d,%d,%d) rejected: %v", o.u, o.v, o.w, err)
			}
			merged, unbridged := ng.add(o.u, o.v, o.w, ver)

			if res.ID != ver || res.Version != ver {
				fail("op %d: id/version=%d/%d, want %d", ver, res.ID, res.Version, ver)
			}
			if res.Kind != wantKind {
				fail("op %d: Kind=%s, naive says %s", ver, res.Kind, wantKind)
			}
			// Compare the reported merge facts.
			if res.Kind == KindMerge {
				if !reflect.DeepEqual(res.Merged, merged) {
					fail("op %d: Merged=%v, naive says %v", ver, res.Merged, merged)
				}
				if !reflect.DeepEqual(res.Unbridged, unbridged) {
					fail("op %d: Unbridged=%v, naive says %v", ver, res.Unbridged, unbridged)
				}
				if len(merged) > 0 && res.NewLabel != merged[0] {
					fail("op %d: NewLabel=%d, naive says %d", ver, res.NewLabel, merged[0])
				}
				if w := ng.bWeight[ng.blockOf[o.u]]; res.NewWeight != w {
					fail("op %d: NewWeight=%d, naive says %d", ver, res.NewWeight, w)
				}
			} else {
				if len(res.Merged) != 0 || len(res.Unbridged) != 0 || res.NewWeight != 0 {
					fail("op %d: %s must report empty merge facts, got %+v", ver, res.Kind, res)
				}
				if len(unbridged) != 0 {
					fail("op %d: %s but naive unbridged %v", ver, res.Kind, unbridged)
				}
			}

			// Counts.
			wantBridges, wantBlocks, wantComps := 0, 0, 0
			for e := 1; e <= ver; e++ {
				if ng.isBridge[e] {
					wantBridges++
				}
			}
			seenB, seenC := map[int]bool{}, map[int]bool{}
			for x := 0; x < n; x++ {
				if !seenB[ng.blockOf[x]] {
					seenB[ng.blockOf[x]] = true
					wantBlocks++
				}
				if !seenC[ng.compOf[x]] {
					seenC[ng.compOf[x]] = true
					wantComps++
				}
			}
			if m.BridgeCount() != wantBridges || m.BlockCount() != wantBlocks ||
				m.ComponentCount() != wantComps {
				fail("op %d: counts=(%d,%d,%d), naive=(%d,%d,%d)", ver,
					m.BridgeCount(), m.BlockCount(), m.ComponentCount(),
					wantBridges, wantBlocks, wantComps)
			}
			if m.BlockCount() != m.BridgeCount()+m.ComponentCount() {
				fail("op %d: forest invariant broken", ver)
			}

			// Per-edge and per-node facts.
			for e := 1; e <= ver; e++ {
				got, err := m.IsBridge(e)
				if err != nil || got != ng.isBridge[e] {
					fail("op %d: IsBridge(%d)=(%v,%v), naive=%v", ver, e, got, err, ng.isBridge[e])
				}
			}
			for x := 0; x < n; x++ {
				l, err := m.Block(x)
				if err != nil || l != ng.blockOf[x] {
					fail("op %d: Block(%d)=(%d,%v), naive=%d", ver, x, l, err, ng.blockOf[x])
				}
				w, err := m.BlockWeight(x)
				if err != nil || w != ng.bWeight[ng.blockOf[x]] {
					fail("op %d: BlockWeight(%d)=(%d,%v), naive=%d", ver, x, w, err, ng.bWeight[ng.blockOf[x]])
				}
			}

			// Sampled path queries.
			for q := 0; q < 3; q++ {
				u, v := rng.Intn(n), rng.Intn(n)
				want := ng.bridgesOnPath(u, v)
				got, err := m.BridgesOnPath(u, v)
				if ng.compOf[u] != ng.compOf[v] {
					if err == nil {
						fail("op %d: BridgesOnPath(%d,%d)=%v, want not-connected", ver, u, v, got)
					}
					continue
				}
				if err != nil || !equalInts(got, want) {
					fail("op %d: BridgesOnPath(%d,%d)=(%v,%v), naive=%v", ver, u, v, got, err, want)
				}
				id, w, err := m.MinBridge(u, v)
				if len(want) == 0 {
					if err == nil {
						fail("op %d: MinBridge(%d,%d)=(%d,%d), want no-bridge", ver, u, v, id, w)
					}
					continue
				}
				wantID := want[0]
				for _, e := range want[1:] {
					if ng.ws[e] < ng.ws[wantID] || (ng.ws[e] == ng.ws[wantID] && e < wantID) {
						wantID = e
					}
				}
				if err != nil || id != wantID || w != int64(ng.ws[wantID]) {
					fail("op %d: MinBridge(%d,%d)=(%d,%d,%v), naive=(%d,%d)",
						ver, u, v, id, w, err, wantID, ng.ws[wantID])
				}
			}
		}

		// Full history check at the end of the sequence.
		for e := 1; e <= len(script); e++ {
			a, f, err := m.EdgeHistory(e)
			if err != nil || a != ng.added[e] || f != ng.free[e] {
				t.Logf("seq %d input: n=%d ops=%v", seq, n, script)
				t.Fatalf("seq %d: EdgeHistory(%d)=(%d,%d,%v), naive=(%d,%d)",
					seq, e, a, f, err, ng.added[e], ng.free[e])
			}
		}
		t.Logf("seq %d ok: n=%d ops=%v -> bridges=%d blocks=%d comps=%d "+
			"(judged by full low-point recomputation after every insertion)",
			seq, n, script, m.BridgeCount(), m.BlockCount(), m.ComponentCount())
	}
}

// equalInts compares two id lists treating nil and empty as equal.
func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
