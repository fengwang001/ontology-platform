package dynamicforest

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"testing"
)

type oracleEdge struct {
	id     int
	u      int
	v      int
	weight int64
}

type oracleState struct {
	n           int
	edges       map[int]oracleEdge
	tree        map[int]bool
	totalWeight int64
	components  int
	since       map[int]int
	version     int
}

func newOracleState(n int) *oracleState {
	return &oracleState{
		n:          n,
		edges:      make(map[int]oracleEdge),
		tree:       make(map[int]bool),
		components: n,
		since:      make(map[int]int),
	}
}

func (state *oracleState) kruskal() (map[int]bool, int64, int) {
	edges := make([]oracleEdge, 0, len(state.edges))
	for _, edge := range state.edges {
		edges = append(edges, edge)
	}
	sort.Slice(edges, func(i int, j int) bool {
		if edges[i].weight != edges[j].weight {
			return edges[i].weight < edges[j].weight
		}
		return edges[i].id < edges[j].id
	})

	parent := make([]int, state.n)
	size := make([]int, state.n)
	for node := range parent {
		parent[node] = node
		size[node] = 1
	}
	var find func(int) int
	find = func(node int) int {
		for parent[node] != node {
			parent[node] = parent[parent[node]]
			node = parent[node]
		}
		return node
	}

	tree := make(map[int]bool, len(edges))
	var totalWeight int64
	components := state.n
	for _, edge := range edges {
		rootU := find(edge.u)
		rootV := find(edge.v)
		if rootU == rootV {
			continue
		}
		if size[rootU] < size[rootV] {
			rootU, rootV = rootV, rootU
		}
		parent[rootV] = rootU
		size[rootU] += size[rootV]
		tree[edge.id] = true
		totalWeight += edge.weight
		components--
	}
	return tree, totalWeight, components
}

func (state *oracleState) update(entered []int, left []int) {
	oldTree := state.tree
	newTree, totalWeight, components := state.kruskal()
	state.version++
	for id := range oldTree {
		if !newTree[id] && !containsID(left, id) {
			panic("oracle left mismatch")
		}
	}
	for id := range newTree {
		if !oldTree[id] && !containsID(entered, id) {
			panic("oracle entered mismatch")
		}
	}
	for _, id := range left {
		state.since[id] = 0
	}
	for _, id := range entered {
		state.since[id] = state.version
	}
	state.tree = newTree
	state.totalWeight = totalWeight
	state.components = components
}

func containsID(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func treeIDs(state *oracleState) []int {
	values := make([]int, 0, len(state.tree))
	for id := range state.tree {
		values = append(values, id)
	}
	sort.Ints(values)
	return values
}

func serviceTreeIDs(service *Service) []int {
	service.mu.Lock()
	defer service.mu.Unlock()
	values := make([]int, 0)
	for id, edge := range service.edges {
		if id == 0 || edge == nil || !edge.alive || !edge.inForest {
			continue
		}
		values = append(values, id)
	}
	sort.Ints(values)
	return values
}

func serviceSince(service *Service) map[int]int {
	service.mu.Lock()
	defer service.mu.Unlock()
	values := make(map[int]int)
	for id, edge := range service.edges {
		if id == 0 || edge == nil || !edge.alive {
			continue
		}
		values[id] = edge.since
	}
	return values
}

func assertServiceInvariants(t *testing.T, service *Service, logger *logBuilder) {
	t.Helper()
	treeEdges := 0
	for id, edge := range service.edges {
		if id == 0 || edge == nil || !edge.alive {
			continue
		}
		if edge.inForest {
			treeEdges++
		} else {
			first := vertexNode(edge.u)
			second := vertexNode(edge.v)
			if !service.forestTree.connected(first, second) {
				t.Fatalf("input=%s non-tree edge %d endpoints disconnected", logger.String(), edge.id)
			}
			maxID, maxWeight := service.forestTree.pathMax(first, second)
			if !greaterEdge(edge.id, edge.weight, maxID, maxWeight) {
				t.Fatalf("input=%s cycle property fails for edge %d vs path max %d", logger.String(), edge.id, maxID)
			}
		}
	}

	expectedTreeEdges := service.nodeCount - service.components
	if treeEdges != expectedTreeEdges {
		t.Fatalf("input=%s forest edge count=%d want=%d", logger.String(), treeEdges, expectedTreeEdges)
	}

	for _, edge := range service.edges {
		if edge == nil || !edge.alive || !edge.inForest {
			continue
		}
		mark := map[int]bool{edge.u: true}
		queue := []int{edge.u}
		for len(queue) > 0 {
			node := queue[0]
			queue = queue[1:]
			for candidateID := range service.forestAdj[node] {
				candidate := service.edges[candidateID]
				if candidate.id == edge.id {
					continue
				}
				other := candidate.u
				if candidate.u == node {
					other = candidate.v
				}
				if !mark[other] {
					mark[other] = true
					queue = append(queue, other)
				}
			}
		}

		for _, candidate := range service.edges {
			if candidate == nil || !candidate.alive || candidate.inForest || candidate.id == edge.id {
				continue
			}
			if mark[candidate.u] != mark[candidate.v] {
				if !greaterEdge(candidate.id, candidate.weight, edge.id, edge.weight) {
					t.Fatalf("input=%s cut property fails for tree edge %d and crossing edge %d", logger.String(), edge.id, candidate.id)
				}
			}
		}
	}
}

func TestRandomSequencesAgainstKruskal(t *testing.T) {
	if testing.Short() {
		t.Skip("random Kruskal comparison is disabled in short mode")
	}

	const sequences = 2000
	logger := logBuilder{}
	for sequence := 0; sequence < sequences; sequence++ {
		rng := rand.New(rand.NewPCG(uint64(sequence+1), 918_271_828))
		n := 2 + rng.IntN(8)
		limit := 1 + rng.IntN(18)
		service, err := NewService(n, limit)
		if err != nil {
			t.Fatal(err)
		}
		oracle := newOracleState(n)
		logger.Reset()
		logger.Line(fmt.Sprintf("seed=%d N=%d E=%d", sequence+1, n, limit))

		steps := 30 + rng.IntN(41)
		for step := 0; step < steps; step++ {
			alive := len(oracle.edges)
			choice := rng.IntN(100)
			switch {
			case choice < 48 && alive < limit:
				u := rng.IntN(n)
				v := rng.IntN(n)
				weight := int64(rng.IntN(21) - 10)
				logger.Line(fmt.Sprintf("step=%d AddEdge(%d,%d,%d)", step, u, v, weight))
				id, result, addErr := service.AddEdge(u, v, weight)
				if u == v {
					if addErr == nil || addErr != ErrInvalidArgument {
						t.Fatalf("input=%s error=%v", logger.String(), addErr)
					}
					logger.Line(fmt.Sprintf("output=rejected invalid: %v; basis=u==v", addErr))
					continue
				}
				if addErr != nil {
					t.Fatalf("input=%s unexpected add error: %v", logger.String(), addErr)
				}
				oracle.edges[id] = oracleEdge{id: id, u: u, v: v, weight: weight}
				oracle.update(result.Entered, result.Left)
				logger.Line(fmt.Sprintf("output=id:%d %+v; basis=Kruskal weights then IDs", id, result))
			case alive > 0 && choice < 78:
				ids := make([]int, 0, alive)
				for id := range oracle.edges {
					ids = append(ids, id)
				}
				sort.Ints(ids)
				id := ids[rng.IntN(len(ids))]
				weight := int64(rng.IntN(21) - 10)
				logger.Line(fmt.Sprintf("step=%d SetWeight(%d,%d)", step, id, weight))
				result, setErr := service.SetWeight(id, weight)
				if setErr != nil {
					t.Fatalf("input=%s unexpected set error: %v", logger.String(), setErr)
				}
				edge := oracle.edges[id]
				edge.weight = weight
				oracle.edges[id] = edge
				oracle.update(result.Entered, result.Left)
				logger.Line(fmt.Sprintf("output=%+v; basis=compare old and new unique Kruskal forest", result))
			case alive > 0:
				ids := make([]int, 0, alive)
				for id := range oracle.edges {
					ids = append(ids, id)
				}
				sort.Ints(ids)
				id := ids[rng.IntN(len(ids))]
				logger.Line(fmt.Sprintf("step=%d RemoveEdge(%d)", step, id))
				result, removeErr := service.RemoveEdge(id)
				if removeErr != nil {
					t.Fatalf("input=%s unexpected remove error: %v", logger.String(), removeErr)
				}
				delete(oracle.edges, id)
				delete(oracle.since, id)
				oracle.update(result.Entered, result.Left)
				logger.Line(fmt.Sprintf("output=%+v; basis=delete edge and rerun Kruskal", result))
			default:
				step--
				continue
			}

			if result := service.Version(); result != oracle.version {
				t.Fatalf("input=%s version service=%d oracle=%d", logger.String(), result, oracle.version)
			}
			if result := service.Weight(); result != oracle.totalWeight {
				t.Fatalf("input=%s weight service=%d oracle=%d", logger.String(), result, oracle.totalWeight)
			}
			if result := service.Components(); result != oracle.components {
				t.Fatalf("input=%s components service=%d oracle=%d", logger.String(), result, oracle.components)
			}
			gotTree := serviceTreeIDs(service)
			wantTree := treeIDs(oracle)
			if !reflectDeepEqual(gotTree, wantTree) {
				t.Fatalf("input=%s tree service=%v oracle=%v", logger.String(), gotTree, wantTree)
			}
			gotSince := serviceSince(service)
			for id, want := range oracle.since {
				if gotSince[id] != want {
					t.Fatalf("input=%s TreeSince(%d) service=%d oracle=%d", logger.String(), id, gotSince[id], want)
				}
			}
			assertServiceInvariants(t, service, &logger)
		}
		if testing.Verbose() {
			t.Log(logger.String())
		}
	}
}

type logBuilder struct {
	lines []string
}

func (builder *logBuilder) Reset() {
	builder.lines = builder.lines[:0]
}

func (builder *logBuilder) Line(value string) {
	builder.lines = append(builder.lines, value)
}

func (builder *logBuilder) String() string {
	return strings.Join(builder.lines, "\n")
}

func reflectDeepEqual(first []int, second []int) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if first[index] != second[index] {
			return false
		}
	}
	return true
}
