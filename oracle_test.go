package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

type oracleEdge struct {
	u, v      int
	weight    int64
	born      int
	unbridged int
}

type oracleState struct {
	n      int
	edges  []oracleEdge
	bridge map[int]bool
}

type testDSU struct {
	parent []int
}

func newTestDSU(n int) *testDSU {
	d := &testDSU{parent: make([]int, n)}
	for i := range d.parent {
		d.parent[i] = i
	}
	return d
}

func (d *testDSU) find(x int) int {
	for d.parent[x] != x {
		d.parent[x] = d.parent[d.parent[x]]
		x = d.parent[x]
	}
	return x
}

func (d *testDSU) union(a, b int) {
	a, b = d.find(a), d.find(b)
	if a != b {
		d.parent[b] = a
	}
}

func newOracle(n int) *oracleState {
	return &oracleState{n: n, edges: []oracleEdge{{}}, bridge: map[int]bool{}}
}

func (o *oracleState) recompute() {
	adj := make([][]struct{ to, id int }, o.n)
	for id := 1; id < len(o.edges); id++ {
		e := o.edges[id]
		adj[e.u] = append(adj[e.u], struct{ to, id int }{e.v, id})
		adj[e.v] = append(adj[e.v], struct{ to, id int }{e.u, id})
	}
	seen := make([]bool, o.n)
	tin := make([]int, o.n)
	low := make([]int, o.n)
	parentEdge := make([]int, o.n)
	for i := range parentEdge {
		parentEdge[i] = -1
	}
	timer := 0
	o.bridge = map[int]bool{}
	var dfs func(int)
	dfs = func(v int) {
		seen[v] = true
		timer++
		tin[v], low[v] = timer, timer
		for _, next := range adj[v] {
			if next.id == parentEdge[v] {
				continue
			}
			if seen[next.to] {
				if tin[next.to] < low[v] {
					low[v] = tin[next.to]
				}
			} else {
				parentEdge[next.to] = next.id
				dfs(next.to)
				if low[next.to] < low[v] {
					low[v] = low[next.to]
				}
				if low[next.to] > tin[v] {
					o.bridge[next.id] = true
				}
			}
		}
	}
	for v := 0; v < o.n; v++ {
		if !seen[v] {
			dfs(v)
		}
	}
}

func (o *oracleState) dsuUsing(skip map[int]bool) *testDSU {
	d := newTestDSU(o.n)
	for id := 1; id < len(o.edges); id++ {
		if !skip[id] {
			d.union(o.edges[id].u, o.edges[id].v)
		}
	}
	return d
}

func (o *oracleState) labels(d *testDSU) map[int]int {
	labels := map[int]int{}
	for v := 0; v < o.n; v++ {
		root := d.find(v)
		if label, ok := labels[root]; !ok || v < label {
			labels[root] = v
		}
	}
	return labels
}

func (o *oracleState) pathBridges(u, v int, components *testDSU, bridges map[int]bool, maxID ...int) ([]int, bool) {
	if components.find(u) != components.find(v) {
		return nil, false
	}
	limit := len(o.edges)
	if len(maxID) > 0 {
		limit = maxID[0] + 1
	}
	adj := make([][]struct{ to, id int }, o.n)
	for id := 1; id < limit; id++ {
		e := o.edges[id]
		adj[e.u] = append(adj[e.u], struct{ to, id int }{e.v, id})
		adj[e.v] = append(adj[e.v], struct{ to, id int }{e.u, id})
	}
	parentNode := make([]int, o.n)
	parentEdge := make([]int, o.n)
	for i := range parentNode {
		parentNode[i], parentEdge[i] = -1, -1
	}
	parentNode[u] = u
	queue := []int{u}
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		for _, next := range adj[x] {
			if parentNode[next.to] < 0 {
				parentNode[next.to], parentEdge[next.to] = x, next.id
				queue = append(queue, next.to)
			}
		}
	}
	ids := []int{}
	for x := v; x != u; x = parentNode[x] {
		if id := parentEdge[x]; bridges[id] {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	return ids, true
}

func (o *oracleState) blockWeight(x int, blocks *testDSU) int64 {
	root := blocks.find(x)
	total := int64(0)
	for id := 1; id < len(o.edges); id++ {
		e := o.edges[id]
		if !o.bridge[id] && blocks.find(e.u) == root {
			total += e.weight
		}
	}
	return total
}

func sortedBridgeIDs(bridges map[int]bool) []int {
	ids := make([]int, 0, len(bridges))
	for id := range bridges {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

func TestRandomOracle2000(t *testing.T) {
	for seed := int64(1); seed <= 2000; seed++ {
		rng := rand.New(rand.NewSource(seed))
		n := 1 + rng.Intn(14)
		limit := 25 + rng.Intn(70)
		m, err := NewIncrementalBridges(n, limit)
		if err != nil {
			t.Fatal(err)
		}
		o := newOracle(n)
		accepted := 0
		for call := 0; call < limit+4; call++ {
			u, v := rng.Intn(n), rng.Intn(n)
			w := int64(1 + rng.Intn(5))
			o.recompute()
			beforeComponents := o.dsuUsing(nil)
			beforeBlocks := o.dsuUsing(o.bridge)
			beforeBridges := map[int]bool{}
			for id, bridge := range o.bridge {
				beforeBridges[id] = bridge
			}
			input := fmt.Sprintf("seed=%d call=%d AddEdge(%d,%d,%d)", seed, call, u, v, w)
			result, addErr := m.AddEdge(u, v, w)
			if u == v {
				if addErr != ErrSelfLoop {
					t.Fatalf("%s error=%v", input, addErr)
				}
				t.Logf("%s => rejected self-loop", input)
				continue
			}
			if accepted >= limit {
				if addErr != ErrEdgeLimit {
					t.Fatalf("%s error=%v", input, addErr)
				}
				t.Logf("%s => rejected edge-limit", input)
				continue
			}
			if addErr != nil {
				t.Fatalf("%s unexpected %v", input, addErr)
			}
			accepted++
			o.edges = append(o.edges, oracleEdge{u: u, v: v, weight: w, born: result.Version})

			wantKind := KindInside
			var wantMerged, wantUnbridged []int
			wantWeight := int64(0)
			switch {
			case beforeComponents.find(u) != beforeComponents.find(v):
				wantKind = KindLink
			case beforeBlocks.find(u) == beforeBlocks.find(v):
				wantKind = KindInside
			default:
				wantKind = KindMerge
				path, _ := o.pathBridges(u, v, beforeComponents, beforeBridges, len(o.edges)-2)
				wantUnbridged = path
				labelSet := map[int]struct{}{}
				labelSet[o.labels(beforeBlocks)[beforeBlocks.find(u)]] = struct{}{}
				labelSet[o.labels(beforeBlocks)[beforeBlocks.find(v)]] = struct{}{}
				for _, id := range path {
					e := o.edges[id]
					labelSet[o.labels(beforeBlocks)[beforeBlocks.find(e.u)]] = struct{}{}
					labelSet[o.labels(beforeBlocks)[beforeBlocks.find(e.v)]] = struct{}{}
					wantWeight += e.weight
				}
				for label := range labelSet {
					wantMerged = append(wantMerged, label)
					root := beforeBlocks.find(label)
					for oldID := 1; oldID < len(o.edges)-1; oldID++ {
						oldEdge := o.edges[oldID]
						if !o.bridge[oldID] && beforeBlocks.find(oldEdge.u) == root {
							wantWeight += oldEdge.weight
						}
					}
				}
				sort.Ints(wantMerged)
				wantWeight += w
			}
			if result.Kind != wantKind || !reflect.DeepEqual(result.Merged, wantMerged) ||
				!reflect.DeepEqual(result.Unbridged, wantUnbridged) || result.NewWeight != wantWeight {
				t.Fatalf("%s => %+v; oracle kind=%s merged=%v unbridged=%v weight=%d",
					input, result, wantKind, wantMerged, wantUnbridged, wantWeight)
			}
			if wantKind == KindMerge && result.NewLabel != wantMerged[0] {
				t.Fatalf("%s NewLabel=%d want %d", input, result.NewLabel, wantMerged[0])
			}

			o.recompute()
			components := o.dsuUsing(nil)
			blocks := o.dsuUsing(o.bridge)
			componentLabels := o.labels(components)
			blockLabels := o.labels(blocks)
			bridgeCount := len(o.bridge)
			nonBridgeWeight := int64(0)
			for id := 1; id < len(o.edges); id++ {
				got, err := m.IsBridge(id)
				if err != nil || got != o.bridge[id] {
					t.Fatalf("%s IsBridge(%d)=%v,%v oracle=%v", input, id, got, err, o.bridge[id])
				}
				if !o.bridge[id] {
					nonBridgeWeight += o.edges[id].weight
				}
				born, unbridged, err := m.EdgeHistory(id)
				if err != nil || born != o.edges[id].born {
					t.Fatalf("%s EdgeHistory born(%d)=%d,%v oracle=%d", input, id, born, err, o.edges[id].born)
				}
				if !o.bridge[id] {
					if o.edges[id].unbridged == 0 {
						o.edges[id].unbridged = result.Version
					}
					if unbridged != o.edges[id].unbridged {
						t.Fatalf("%s EdgeHistory unbridged(%d)=%d want %d", input, id, unbridged, o.edges[id].unbridged)
					}
				}
				if o.bridge[id] && unbridged != 0 {
					t.Fatalf("%s EdgeHistory unbridged(%d)=%d want 0", input, id, unbridged)
				}
			}
			if m.BridgeCount() != bridgeCount || m.ComponentCount() != len(componentLabels) ||
				m.BlockCount() != len(blockLabels) || m.BridgeCount()+m.ComponentCount() != m.BlockCount() {
				t.Fatalf("%s counts bridges=%d/%d blocks=%d/%d comps=%d/%d",
					input, m.BridgeCount(), bridgeCount, m.BlockCount(), len(blockLabels), m.ComponentCount(), len(componentLabels))
			}
			actualWeightSum := int64(0)
			for x := 0; x < o.n; x++ {
				root := blocks.find(x)
				wantLabel := blockLabels[root]
				gotLabel, err := m.Block(x)
				if err != nil || gotLabel != wantLabel {
					t.Fatalf("%s Block(%d)=%d,%v want %d", input, x, gotLabel, err, wantLabel)
				}
				if wantLabel == x {
					wantWeight := o.blockWeight(x, blocks)
					gotWeight, err := m.BlockWeight(x)
					if err != nil || gotWeight != wantWeight {
						t.Fatalf("%s BlockWeight(%d)=%d,%v want %d", input, x, gotWeight, err, wantWeight)
					}
					actualWeightSum += gotWeight
				}
			}
			if actualWeightSum != nonBridgeWeight {
				t.Fatalf("%s block weight sum %d oracle %d", input, actualWeightSum, nonBridgeWeight)
			}
			for x := 0; x < o.n; x++ {
				for y := x; y < o.n; y++ {
					wantConnected := components.find(x) == components.find(y)
					gotConnected, err := m.Connected(x, y)
					if err != nil || gotConnected != wantConnected {
						t.Fatalf("%s Connected(%d,%d)=%v want %v", input, x, y, gotConnected, wantConnected)
					}
					path, err := m.BridgesOnPath(x, y)
					wantPath, pathOK := o.pathBridges(x, y, components, o.bridge)
					if !pathOK {
						if err != ErrNotConnected {
							t.Fatalf("%s path error %v", input, err)
						}
						continue
					}
					if err != nil || !reflect.DeepEqual(path, wantPath) {
						t.Fatalf("%s BridgesOnPath(%d,%d)=%v,%v want %v", input, x, y, path, err, wantPath)
					}
					min, err := m.MinBridge(x, y)
					if len(wantPath) == 0 {
						if err != ErrNoBridgeOnPath {
							t.Fatalf("%s MinBridge error=%v value=%+v", input, err, min)
						}
						continue
					}
					wantID := -1
					for _, id := range wantPath {
						if wantID < 0 || o.edges[id].weight < o.edges[wantID].weight ||
							(o.edges[id].weight == o.edges[wantID].weight && id < wantID) {
							wantID = id
						}
					}
					if err != nil || min.ID != wantID || min.Weight != o.edges[wantID].weight {
						t.Fatalf("%s MinBridge(%d,%d)=%+v,%v want %d", input, x, y, min, err, wantID)
					}
				}
			}
			t.Logf("%s => %+v; Tarjan bridges=%v", input, result, sortedBridgeIDs(o.bridge))
		}
	}
}
