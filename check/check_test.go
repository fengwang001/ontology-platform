package check

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"ontology/dij"
	"ontology/graph"
)

type tc struct {
	name   string
	n, src int
	edges  []graph.Edge
	want   error
}

func e(from, to int, w float64) graph.Edge { return graph.Edge{From: from, To: to, W: w} }

var cases = []tc{
	{"single node", 1, 0, nil, nil}, {"no edges", 4, 2, nil, nil},
	{"multi improve", 3, 0, []graph.Edge{e(0, 1, 10), e(0, 2, 1), e(2, 1, 1)}, nil}, {"equal paths", 4, 0, []graph.Edge{e(0, 1, 1), e(0, 2, 1), e(1, 3, 1), e(2, 3, 1)}, nil},
}

func TestMatchesBellmanFord(t *testing.T) { // +Inf == +Inf，slices.Equal 可直接比较
	for _, c := range cases {
		got, err := dij.ShortestPath(c.n, c.edges, c.src)
		if want := BellmanFord(c.n, c.edges, c.src); err != nil || !slices.Equal(got, want) {
			t.Errorf("%s: dist=%v, want %v (err=%v)", c.name, got, want, err)
		}
	}
}

func TestErrors(t *testing.T) {
	for _, c := range []tc{
		{"negative edge", 2, 0, []graph.Edge{e(0, 1, -1)}, dij.ErrNegativeEdge}, {"src too big", 2, 2, nil, dij.ErrBadSrc}, {"edge to bad", 2, 0, []graph.Edge{e(0, 2, 1)}, dij.ErrBadEdge},
	} {
		if _, err := dij.ShortestPath(c.n, c.edges, c.src); !errors.Is(err, c.want) {
			t.Errorf("%s: err=%v, want %v", c.name, err, c.want)
		}
	}
}

// buggy 是内联的错误实现：弹出过期记录时不跳过，直接用其距离定案并更新邻居。
func buggy(n int, edges []graph.Edge, src int) []float64 {
	adj := make([][]graph.Edge, n)
	for _, ed := range edges {
		adj[ed.From] = append(adj[ed.From], ed)
	}
	dist, done := make([]float64, n), make([]bool, n)
	qn, qd := []int{src}, []float64{0}
	for len(qn) > 0 {
		b := slices.Index(qd, slices.Min(qd))
		node, d := qn[b], qd[b]
		qn, qd = slices.Delete(qn, b, b+1), slices.Delete(qd, b, b+1)
		dist[node], done[node] = d, true // 错误：过期记录覆盖已定案距离
		for _, ed := range adj[node] {
			if !done[ed.To] {
				qn, qd = append(qn, ed.To), append(qd, d+ed.W)
			}
		}
	}
	return dist
}

func TestStaleRecordsSkipped(t *testing.T) { // 节点 1 被改进两次（10 -> 2）
	if got := buggy(3, cases[2].edges, 0); got[1] == 2 {
		t.Fatalf("buggy dist[1]=%v; stale-entry bug not exercised", got[1])
	}
}

func TestRelaxationBound(t *testing.T) {
	const n, m = 1000, 5000
	edges := make([]graph.Edge, m)
	for i := range edges {
		edges[i] = e(i%n, (i*31+17)%n, float64(i%7+1))
	}
	before := dij.Relaxations()
	_, _ = dij.ShortestPath(n, edges, 0)
	if r := dij.Relaxations() - before; r > m {
		t.Fatalf("relaxations=%d exceeds m=%d", r, m)
	}
}

func TestConcurrentDeterministic(t *testing.T) {
	want, _ := dij.ShortestPath(4, cases[len(cases)-1].edges, 0)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			got, _ := dij.ShortestPath(4, cases[len(cases)-1].edges, 0)
			if !slices.Equal(got, want) {
				t.Errorf("dist=%v, want %v", got, want)
			}
		})
	}
	wg.Wait()
}
