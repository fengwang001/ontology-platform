package ontology

import (
	"bytes"
	"math/rand"
	"reflect"
	"testing"
)

func makeNode(src, dst, length int64) copyNode {
	return copyNode{src: src, dst: dst, len: length}
}

func naiveIntervals(nodes []copyNode) ([][]int, int64) {
	adj := make([][]int, len(nodes))
	var compares int64
	for a := range nodes {
		for b := range nodes {
			if a == b {
				continue
			}
			compares++
			if nodes[a].src < nodes[b].dst+nodes[b].len && nodes[b].dst < nodes[a].src+nodes[a].len {
				adj[a] = append(adj[a], b)
			}
		}
	}
	return adj, compares
}

func naiveSchedule(nodes []copyNode, adj [][]int) copySchedule {
	k := len(nodes)
	active := make([]bool, k)
	indegree := make([]int, k)
	for from := range adj {
		active[from] = true
		for _, to := range adj[from] {
			indegree[to]++
		}
	}

	copies := make([]int, 0, k)
	stashSet := make([]int, 0)
	var stashBytes int64

	readyNode := func() int {
		chosen := -1
		for node := 0; node < k; node++ {
			if active[node] && indegree[node] == 0 && (chosen < 0 || nodes[node].dst < nodes[chosen].dst) {
				chosen = node
			}
		}
		return chosen
	}

	for {
		for {
			node := readyNode()
			if node < 0 {
				break
			}
			active[node] = false
			copies = append(copies, node)
			for _, next := range adj[node] {
				if active[next] {
					indegree[next]--
				}
			}
		}

		remaining := 0
		for _, isActive := range active {
			if isActive {
				remaining++
			}
		}
		if remaining == 0 {
			break
		}

		components := naiveSCC(adj, active)
		chosen := -1
		for _, component := range components {
			if len(component) < 2 {
				continue
			}
			for _, node := range component {
				if active[node] && (chosen < 0 || lessCopy(nodes[node], nodes[chosen])) {
					chosen = node
				}
			}
		}
		active[chosen] = false
		stashSet = append(stashSet, chosen)
		stashBytes += nodes[chosen].len
		for _, next := range adj[chosen] {
			if active[next] {
				indegree[next]--
			}
		}
	}

	for i := 0; i < len(stashSet); i++ {
		for j := i + 1; j < len(stashSet); j++ {
			if nodes[stashSet[j]].dst < nodes[stashSet[i]].dst {
				stashSet[i], stashSet[j] = stashSet[j], stashSet[i]
			}
		}
	}
	stashes := make([]stashedCopy, len(stashSet))
	for slot, node := range stashSet {
		stashes[slot] = stashedCopy{node: node, slot: slot}
	}
	return copySchedule{copies: copies, stashes: stashes, stashBytes: stashBytes}
}

func naiveSCC(adj [][]int, active []bool) [][]int {
	k := len(adj)
	visit := make([]bool, k)
	order := make([]int, 0, k)
	var dfs func(int)
	dfs = func(node int) {
		visit[node] = true
		for _, next := range adj[node] {
			if active[next] && !visit[next] {
				dfs(next)
			}
		}
		order = append(order, node)
	}
	for node := 0; node < k; node++ {
		if active[node] && !visit[node] {
			dfs(node)
		}
	}

	pred := make([][]int, k)
	for from := 0; from < k; from++ {
		if !active[from] {
			continue
		}
		for _, to := range adj[from] {
			if active[to] {
				pred[to] = append(pred[to], from)
			}
		}
	}

	componentID := make([]int, k)
	for i := range componentID {
		componentID[i] = -1
	}
	var reverseDFS func(int, int)
	reverseDFS = func(node, id int) {
		componentID[node] = id
		for _, prev := range pred[node] {
			if componentID[prev] == -1 {
				reverseDFS(prev, id)
			}
		}
	}
	nextID := 0
	for i := len(order) - 1; i >= 0; i-- {
		if componentID[order[i]] == -1 {
			reverseDFS(order[i], nextID)
			nextID++
		}
	}
	components := make([][]int, nextID)
	for node := 0; node < k; node++ {
		if active[node] {
			components[componentID[node]] = append(components[componentID[node]], node)
		}
	}
	return components
}

func assertSchedulesEqual(t *testing.T, nodes []copyNode, got, want copySchedule) {
	t.Helper()
	gotStash := make([]int, len(got.stashes))
	wantStash := make([]int, len(want.stashes))
	for i := range got.stashes {
		gotStash[i] = got.stashes[i].node
	}
	for i := range want.stashes {
		wantStash[i] = want.stashes[i].node
	}
	if !reflect.DeepEqual(got.copies, want.copies) ||
		!reflect.DeepEqual(gotStash, wantStash) ||
		got.stashBytes != want.stashBytes {
		t.Fatalf("nodes=%#v\n got copies=%v stash=%v bytes=%d\nwant copies=%v stash=%v bytes=%d",
			nodes, got.copies, gotStash, got.stashBytes, want.copies, wantStash, want.stashBytes)
	}
}

func TestSyntheticGraphSchedules(t *testing.T) {
	nodes := []copyNode{
		{dst: 0, len: 1},
		{dst: 1, len: 1},
		{dst: 2, len: 1},
		{dst: 3, len: 1},
		{dst: 4, len: 1},
	}
	tests := []struct {
		name  string
		edges [][2]int
	}{
		{
			name:  "connected cycles with downstream acyclic node",
			edges: [][2]int{{0, 1}, {1, 0}, {1, 2}, {2, 1}, {2, 3}},
		},
		{
			name:  "one node joins two cycles",
			edges: [][2]int{{0, 1}, {1, 0}, {1, 2}, {2, 1}, {2, 3}, {3, 4}, {4, 2}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adj := make([][]int, len(nodes))
			for _, edge := range tt.edges {
				adj[edge[0]] = append(adj[edge[0]], edge[1])
			}
			got := scheduleGraph(nodes, adj)
			want := naiveSchedule(nodes, adj)
			assertSchedulesEqual(t, nodes, got, want)
		})
	}
}

func TestStashTieLengthThenDst(t *testing.T) {
	nodes := []copyNode{
		makeNode(10, 0, 1),
		makeNode(11, 1, 2),
		makeNode(12, 2, 2),
		makeNode(13, 3, 1),
		makeNode(14, 4, 1),
	}
	adj := [][]int{{1}, {2}, {0}, {4}, {3}}
	got := scheduleGraph(nodes, adj)
	want := naiveSchedule(nodes, adj)
	assertSchedulesEqual(t, nodes, got, want)
	if len(want.stashes) != 2 || want.stashes[0].node != 0 || want.stashes[1].node != 3 {
		t.Fatalf("expected len-tied cycles to choose nodes 0 then 3, got %#v", want.stashes)
	}
}

func TestRandomIntervalSchedulesAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(1255))
	for iter := 0; iter < 2000; iter++ {
		n := int64(1 + rng.Intn(18))
		maxSize := int64(64)
		m := int64(0)
		nodes := make([]copyNode, 0)
		old := []byte("abcdefghijklmnopqr")
		delta := make([]Instr, 0)

		instructionCount := 1 + rng.Intn(12)
		for instruction := 0; instruction < instructionCount && m < maxSize; instruction++ {
			if rng.Intn(5) == 0 && maxSize-m > 0 {
				length := 1 + rng.Intn(int(maxSize-m))
				data := bytes.Repeat([]byte{'A' + byte(iter%26)}, length)
				delta = append(delta, Instr{Kind: AddInstr, Data: data})
				m += int64(length)
				continue
			}
			length := int64(1 + rng.Intn(int(n)))
			remaining := maxSize - m
			if length > remaining {
				length = remaining
			}
			if length < 1 {
				break
			}
			src := int64(rng.Intn(int(n - length + 1)))
			delta = append(delta, Instr{Kind: CopyInstr, Src: src, Len: length})
			if src != m {
				nodes = append(nodes, makeNode(src, m, length))
			}
			m += length
		}

		naiveAdj, naiveCompares := naiveIntervals(nodes)
		var efficientCompares int64
		efficientAdj := buildIntervals(nodes, &efficientCompares)
		got := scheduleCopies(nodes)
		want := naiveSchedule(nodes, naiveAdj)
		edgeCount := 0
		for _, neighbors := range naiveAdj {
			edgeCount += len(neighbors)
		}
		t.Logf("random case %04d input n=%d m=%d delta=%#v nodes=%#v edges=%d compares naive=%d efficient=%d output copies=%v stashes=%#v decision=byte-execution plus naive-schedule-equality",
			iter, n, m, delta, nodes, edgeCount, naiveCompares, efficientCompares, got.copies, got.stashes)

		if !reflect.DeepEqual(efficientAdj, naiveAdj) {
			t.Fatalf("case %d adjacency mismatch:\nnodes=%#v\ngot=%v\nwant=%v", iter, nodes, efficientAdj, naiveAdj)
		}
		assertSchedulesEqual(t, nodes, got, want)
		if got.edges != edgeCount {
			t.Fatalf("case %d edges = %d, want %d", iter, got.edges, edgeCount)
		}

		planner := NewPlanner(maxSize, 100)
		plan, err := planner.Plan(n, delta)
		if err != nil {
			t.Fatalf("case %d Plan: %v", iter, err)
		}
		wantFile := expectedFile(old[:n], delta)
		gotFile := executePlan(old[:n], plan, m)
		if !bytes.Equal(gotFile, wantFile) {
			t.Fatalf("case %d execution got %q want %q plan=%#v", iter, gotFile, wantFile, plan.Operations)
		}
	}
}

func TestTwentyHundredSparseComparisonBound(t *testing.T) {
	const k = 2000
	nodes := make([]copyNode, k)
	for i := range nodes {
		dst := int64(i * 2)
		src := int64(1_000_000 + i*2)
		nodes[i] = makeNode(src, dst, 1)
	}
	var compares int64
	adj := buildIntervals(nodes, &compares)
	edges := 0
	for _, neighbors := range adj {
		edges += len(neighbors)
	}
	limit := int64(2) * (int64(k)*ceilLog2(int64(k)+1) + int64(edges))
	if edges != 0 || compares > limit {
		t.Fatalf("edges=%d compares=%d limit=%d", edges, compares, limit)
	}
	if compares >= (k*k)/2 {
		t.Fatalf("compares=%d not far below k²/2=%d", compares, (k*k)/2)
	}
}

func ceilLog2(value int64) int64 {
	var result int64
	for v := int64(1); v < value; v <<= 1 {
		result++
	}
	return result
}
