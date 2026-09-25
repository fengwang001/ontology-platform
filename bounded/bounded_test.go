package bounded

import (
	"errors"
	"slices"
	"testing"

	"ontology/graph"
	"ontology/traverse"
)

func build(t *testing.T, nodes []string, edges [][2]string) *graph.Graph {
	t.Helper()
	g := graph.New()
	for _, n := range nodes {
		if err := g.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range edges {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			t.Fatal(err)
		}
	}
	return g
}

var (
	chainNodes = []string{"A", "B", "C", "D", "E"}
	chainEdges = [][2]string{{"A", "B"}, {"B", "C"}, {"C", "D"}, {"D", "E"}}
	ringNodes  = []string{"A", "B", "C"}
	ringEdges  = [][2]string{{"A", "B"}, {"B", "C"}, {"C", "A"}}
	diaNodes   = []string{"A", "B", "C", "D"}
	diaEdges   = [][2]string{{"A", "B"}, {"A", "C"}, {"B", "D"}, {"C", "D"}}
)

func TestWalk(t *testing.T) {
	cases := []struct {
		name      string
		nodes     []string
		edges     [][2]string
		start     string
		dir       traverse.Direction
		opts      []Option
		wantNodes []string
		wantErr   error // nil = Complete
	}{
		{"chain no bounds", chainNodes, chainEdges, "A", traverse.Out, nil,
			[]string{"A", "B", "C", "D", "E"}, nil},
		{"limit exactly reachable is Complete", chainNodes, chainEdges, "A", traverse.Out,
			[]Option{Limit(5)}, []string{"A", "B", "C", "D", "E"}, nil},
		{"limit below reachable is LimitCut", chainNodes, chainEdges, "A", traverse.Out,
			[]Option{Limit(3)}, []string{"A", "B", "C"}, ErrLimitCut},
		{"limit 1 on chain", chainNodes, chainEdges, "A", traverse.Out,
			[]Option{Limit(1)}, []string{"A"}, ErrLimitCut},
		{"ring exactly fills limit is Complete", ringNodes, ringEdges, "A", traverse.Out,
			[]Option{Limit(3)}, []string{"A", "B", "C"}, nil},
		{"ring overflows limit is LimitCut", ringNodes, ringEdges, "A", traverse.Out,
			[]Option{Limit(2)}, []string{"A", "B"}, ErrLimitCut},
		{"depth covers whole ring is Complete", ringNodes, ringEdges, "A", traverse.Out,
			[]Option{MaxDepth(2)}, []string{"A", "B", "C"}, nil},
		{"depth cuts ring is DepthCut", ringNodes, ringEdges, "A", traverse.Out,
			[]Option{MaxDepth(1)}, []string{"A", "B"}, ErrDepthCut},
		{"depth cuts chain", chainNodes, chainEdges, "A", traverse.Out,
			[]Option{MaxDepth(2)}, []string{"A", "B", "C"}, ErrDepthCut},
		{"depth exactly chain length is Complete", chainNodes, chainEdges, "A", traverse.Out,
			[]Option{MaxDepth(4)}, []string{"A", "B", "C", "D", "E"}, nil},
		{"diamond depth 1", diaNodes, diaEdges, "A", traverse.Out,
			[]Option{MaxDepth(1)}, []string{"A", "B", "C"}, ErrDepthCut},
		{"diamond no bounds", diaNodes, diaEdges, "A", traverse.Out, nil,
			[]string{"A", "B", "C", "D"}, nil},
		{"in-dir limit on chain", chainNodes, chainEdges, "E", traverse.In,
			[]Option{Limit(2)}, []string{"E", "D"}, ErrLimitCut},
		{"both-dir ring depth 1 covers all", ringNodes, ringEdges, "A", traverse.Both,
			[]Option{MaxDepth(1)}, []string{"A", "B", "C"}, nil},
		{"both-dir ring depth 0 cuts", ringNodes, ringEdges, "A", traverse.Both,
			[]Option{MaxDepth(0)}, []string{"A", "B", "C"}, nil}, // 0 = 不限
		{"limit and depth: limit wins when hit first", chainNodes, chainEdges, "A", traverse.Out,
			[]Option{MaxDepth(4), Limit(2)}, []string{"A", "B"}, ErrLimitCut},
		{"limit and depth: depth cut when frontier exhausts", chainNodes, chainEdges, "A", traverse.Out,
			[]Option{MaxDepth(1), Limit(5)}, []string{"A", "B"}, ErrDepthCut},
	}
	for _, tc := range cases {
		g := build(t, tc.nodes, tc.edges)
		res, err := Walk(g, tc.start, tc.dir, tc.opts...)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
			continue
		}
		if !slices.Equal(res.Nodes, tc.wantNodes) {
			t.Errorf("%s: Nodes=%v, want %v", tc.name, res.Nodes, tc.wantNodes)
		}
		// 三态互斥且可 errors.Is 判定。
		if !errors.Is(res.Err, tc.wantErr) {
			t.Errorf("%s: Err=%v, want %v", tc.name, res.Err, tc.wantErr)
		}
		if errors.Is(res.Err, ErrDepthCut) && errors.Is(res.Err, ErrLimitCut) {
			t.Errorf("%s: Err 同时是 DepthCut 与 LimitCut，三态不互斥", tc.name)
		}
		// 不变量：输出 + 未展开 == 可达节点数（环上每节点只贡献一次）。
		all, werr := traverse.Walk(g, tc.start, tc.dir)
		if werr != nil {
			t.Fatalf("%s: %v", tc.name, werr)
		}
		if res.out+res.unexpanded != len(all) {
			t.Errorf("%s: out(%d)+unexpanded(%d) != reachable(%d)",
				tc.name, res.out, res.unexpanded, len(all))
		}
		if res.out != len(res.Nodes) {
			t.Errorf("%s: out(%d) != len(Nodes)(%d)", tc.name, res.out, len(res.Nodes))
		}
	}
}

func TestWalkErrors(t *testing.T) {
	g := build(t, []string{"A"}, nil)
	if _, err := Walk(g, "X", traverse.Out); err == nil {
		t.Error("missing start: want error, got nil")
	}
	if _, err := Walk(g, "A", traverse.Direction(99)); err == nil {
		t.Error("invalid direction: want error, got nil")
	}
}
