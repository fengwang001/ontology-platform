package bounded

import (
	"errors"
	"testing"

	"ontology/graph"
	"ontology/traverse"
)

func build(t *testing.T, nodes []string, edges [][2]string) *graph.Graph {
	t.Helper()
	g := graph.New()
	for _, n := range nodes {
		if err := g.AddNode(n); err != nil {
			t.Fatalf("AddNode(%s): %v", n, err)
		}
	}
	for _, e := range edges {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			t.Fatalf("AddEdge(%s,%s): %v", e[0], e[1], err)
		}
	}
	return g
}

var shapes = map[string]struct {
	nodes []string
	edges [][2]string
}{
	"chain":   {[]string{"a", "b", "c", "d", "e"}, [][2]string{{"a", "b"}, {"b", "c"}, {"c", "d"}, {"d", "e"}}},
	"cycle3":  {[]string{"a", "b", "c"}, [][2]string{{"a", "b"}, {"b", "c"}, {"c", "a"}}},
	"diamond": {[]string{"a", "b", "c", "d"}, [][2]string{{"a", "b"}, {"a", "c"}, {"b", "d"}, {"c", "d"}}},
}

func TestTruncationStates(t *testing.T) {
	cases := []struct {
		name      string
		shape     string
		start     string
		dir       traverse.Dir
		maxDepth  int
		limit     int
		wantNodes int
		wantErr   error // nil = Complete
	}{
		// limit：恰好 == 可达数是 Complete，少 1 才是 LimitCut
		{"limit exact is complete", "chain", "a", traverse.Out, 0, 5, 5, nil},
		{"limit below is cut", "chain", "a", traverse.Out, 0, 4, 4, ErrLimitCut},
		{"limit 1 cuts", "chain", "a", traverse.Out, 0, 1, 1, ErrLimitCut},
		// limit 与环：环去重后恰好占满 limit 是 Complete，占不满才是 LimitCut
		{"cycle exact limit complete", "cycle3", "a", traverse.Out, 0, 3, 3, nil},
		{"cycle limit cut", "cycle3", "a", traverse.Out, 0, 2, 2, ErrLimitCut},
		// 深度：d 覆盖全链 Complete，否则 DepthCut
		{"depth covers chain", "chain", "a", traverse.Out, 4, 0, 5, nil},
		{"depth cuts chain", "chain", "a", traverse.Out, 2, 0, 3, ErrDepthCut},
		{"depth one cuts", "chain", "a", traverse.Out, 1, 0, 2, ErrDepthCut},
		// 深度与环：d >= 环上最长距离时环自然走完，d 更小时 DepthCut
		{"cycle depth sufficient", "cycle3", "a", traverse.Out, 2, 0, 3, nil},
		{"cycle depth huge", "cycle3", "a", traverse.Out, 100, 0, 3, nil},
		{"cycle depth cuts", "cycle3", "a", traverse.Out, 1, 0, 2, ErrDepthCut},
		// 菱形：汇合边指向已发现节点，不误报 DepthCut
		{"diamond depth 1 cuts", "diamond", "a", traverse.Out, 1, 0, 3, ErrDepthCut},
		{"diamond depth 2 complete", "diamond", "a", traverse.Out, 2, 0, 4, nil},
		// 方向：in / both 同样受截断约束
		{"in direction cut", "chain", "e", traverse.In, 1, 0, 2, ErrDepthCut},
		{"both direction limit", "chain", "c", traverse.Both, 0, 3, 3, ErrLimitCut},
		// 无界：零值即无界
		{"unbounded complete", "cycle3", "a", traverse.Out, 0, 0, 3, nil},
	}
	for _, tc := range cases {
		shape := shapes[tc.shape]
		g := build(t, shape.nodes, shape.edges)
		w := New(g, tc.dir, MaxDepth(tc.maxDepth), Limit(tc.limit))
		res, err := w.Walk(tc.start)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !errors.Is(res.Err, tc.wantErr) {
			t.Errorf("%s: Err=%v, want %v", tc.name, res.Err, tc.wantErr)
		}
		if len(res.Nodes) != tc.wantNodes {
			t.Errorf("%s: %d nodes %v, want %d", tc.name, len(res.Nodes), res.Nodes, tc.wantNodes)
		}
		// 三态互斥：Err 不会同时匹配两个哨兵
		if errors.Is(res.Err, ErrLimitCut) && errors.Is(res.Err, ErrDepthCut) {
			t.Errorf("%s: states not mutually exclusive", tc.name)
		}
	}
}

// 不变量：输出数 + 未展开数 == 可达节点数（同深度上界下），环上只贡献一次。
func TestCounterInvariant(t *testing.T) {
	cases := []struct {
		shape    string
		start    string
		dir      traverse.Dir
		maxDepth int
		limit    int
	}{
		{"chain", "a", traverse.Out, 0, 2},
		{"cycle3", "a", traverse.Out, 0, 1},
		{"cycle3", "a", traverse.Out, 0, 2},
		{"diamond", "a", traverse.Out, 0, 3},
		{"chain", "c", traverse.Both, 1, 2},
		{"cycle3", "b", traverse.In, 0, 2},
	}
	for _, tc := range cases {
		shape := shapes[tc.shape]
		g := build(t, shape.nodes, shape.edges)

		w := New(g, tc.dir, MaxDepth(tc.maxDepth), Limit(tc.limit))
		res, err := w.Walk(tc.start)
		if err != nil {
			t.Fatalf("%+v: %v", tc, err)
		}
		output, unexpanded := w.Counters()
		if output != len(res.Nodes) {
			t.Errorf("%+v: output counter %d != %d nodes", tc, output, len(res.Nodes))
		}

		// 可达数 = 同深度上界、不限 limit 的完整遍历
		full := New(g, tc.dir, MaxDepth(tc.maxDepth))
		fullRes, err := full.Walk(tc.start)
		if err != nil {
			t.Fatalf("%+v: %v", tc, err)
		}
		if output+unexpanded != len(fullRes.Nodes) {
			t.Errorf("%+v: output %d + unexpanded %d != reachable %d",
				tc, output, unexpanded, len(fullRes.Nodes))
		}
	}
}

func TestWalkValidation(t *testing.T) {
	g := build(t, shapes["chain"].nodes, shapes["chain"].edges)
	if _, err := New(g, traverse.Dir(99)).Walk("a"); !errors.Is(err, traverse.ErrInvalidDir) {
		t.Errorf("invalid dir: %v", err)
	}
	if _, err := New(g, traverse.Out).Walk("x"); !errors.Is(err, graph.ErrNodeNotFound) {
		t.Errorf("missing start: %v", err)
	}
}
