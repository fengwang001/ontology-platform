// Command demo 逐条打印遍历器语义的 OK/FAIL 判定。
package main

import (
	"errors"
	"fmt"
	"os"
	"slices"

	"ontology/api"
	"ontology/bounded"
	"ontology/graph"
	"ontology/traverse"
)

var failures int

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	checkGraph()
	checkTraverse()
	checkBounded()
	checkAPI()
	if failures > 0 {
		os.Exit(1)
	}
}

func build(nodes []string, edges [][2]string) *graph.Graph {
	g := graph.New()
	for _, n := range nodes {
		if err := g.AddNode(n); err != nil {
			check(fmt.Sprintf("AddNode(%s)", n), false)
		}
	}
	for _, e := range edges {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			check(fmt.Sprintf("AddEdge(%s->%s)", e[0], e[1]), false)
		}
	}
	return g
}

func checkGraph() {
	diamond := build([]string{"A", "B", "C", "D"},
		[][2]string{{"A", "B"}, {"A", "C"}, {"B", "D"}, {"C", "D"}})
	check("graph: 菱形依赖不误报环", !diamond.HasCycle())

	cycle := build([]string{"A", "B", "C"},
		[][2]string{{"A", "B"}, {"B", "C"}, {"C", "A"}})
	check("graph: 真环必报", cycle.HasCycle())

	g := build([]string{"A"}, nil)
	check("graph: 自环被拒绝", g.AddEdge("A", "A") != nil)
	check("graph: 缺节点边被拒绝", g.AddEdge("A", "X") != nil)
	if err := g.AddNode("B"); err == nil {
		if err := g.AddEdge("A", "B"); err == nil {
			check("graph: 重边被拒绝", g.AddEdge("A", "B") != nil)
		}
	}
}

func checkTraverse() {
	g := build([]string{"A", "B", "C", "D", "E"},
		[][2]string{{"A", "B"}, {"A", "C"}, {"B", "D"}, {"C", "D"}, {"D", "E"}})
	out, errOut := traverse.Walk(g, "A", traverse.Out)
	check("traverse: out 方向 BFS 序",
		errOut == nil && slices.Equal(out, []string{"A", "B", "C", "D", "E"}))
	in, errIn := traverse.Walk(g, "E", traverse.In)
	check("traverse: in 方向 BFS 序",
		errIn == nil && slices.Equal(in, []string{"E", "D", "B", "C", "A"}))
	both, errBoth := traverse.Walk(g, "C", traverse.Both)
	check("traverse: both 方向 BFS 序",
		errBoth == nil && slices.Equal(both, []string{"C", "D", "A", "E", "B"}))

	ring := build([]string{"A", "B", "C"},
		[][2]string{{"A", "B"}, {"B", "C"}, {"C", "A"}})
	seq, errRing := traverse.Walk(ring, "A", traverse.Out)
	check("traverse: 环上遍历终止且每节点一次",
		errRing == nil && slices.Equal(seq, []string{"A", "B", "C"}))
}

func checkBounded() {
	chain := build([]string{"A", "B", "C", "D", "E"},
		[][2]string{{"A", "B"}, {"B", "C"}, {"C", "D"}, {"D", "E"}})
	ring := build([]string{"A", "B", "C"},
		[][2]string{{"A", "B"}, {"B", "C"}, {"C", "A"}})

	exact, _ := bounded.Walk(chain, "A", traverse.Out, bounded.Limit(5))
	check("bounded: limit 恰好==可达数记 Complete",
		exact.Err == nil && len(exact.Nodes) == 5)

	cut, _ := bounded.Walk(chain, "A", traverse.Out, bounded.Limit(3))
	check("bounded: limit 截断记 LimitCut",
		errors.Is(cut.Err, bounded.ErrLimitCut) && len(cut.Nodes) == 3)

	ringExact, _ := bounded.Walk(ring, "A", traverse.Out, bounded.Limit(3))
	check("bounded: 环恰好占满 limit 仍记 Complete", ringExact.Err == nil)

	ringCut, _ := bounded.Walk(ring, "A", traverse.Out, bounded.Limit(2))
	check("bounded: 环上 limit 截断记 LimitCut（未走环节点计入未展开）",
		errors.Is(ringCut.Err, bounded.ErrLimitCut))

	depthCut, _ := bounded.Walk(ring, "A", traverse.Out, bounded.MaxDepth(1))
	check("bounded: 深度截断环记 DepthCut",
		errors.Is(depthCut.Err, bounded.ErrDepthCut) &&
			slices.Equal(depthCut.Nodes, []string{"A", "B"}))

	depthFull, _ := bounded.Walk(ring, "A", traverse.Out, bounded.MaxDepth(2))
	check("bounded: 深度恰好覆盖环记 Complete",
		depthFull.Err == nil && len(depthFull.Nodes) == 3)

	check("bounded: 三态互斥且可区分",
		!errors.Is(bounded.ErrDepthCut, bounded.ErrLimitCut) &&
			!errors.Is(bounded.ErrLimitCut, bounded.ErrDepthCut) &&
			!errors.Is(depthCut.Err, bounded.ErrLimitCut) &&
			!errors.Is(cut.Err, bounded.ErrDepthCut))
}

func checkAPI() {
	g := build([]string{"A", "B", "C", "D", "E"},
		[][2]string{{"A", "B"}, {"B", "C"}, {"C", "D"}, {"D", "E"}})

	_, errStart := api.Traverse(api.Request{Graph: g, Start: "X", Dir: api.Out})
	check("api: 起始节点不存在报错", errStart != nil)
	_, errDir := api.Traverse(api.Request{Graph: g, Start: "A", Dir: api.Direction(9)})
	check("api: 非法 dir 报错", errDir != nil)
	_, errDepth := api.Traverse(api.Request{Graph: g, Start: "A", Dir: api.Out, MaxDepth: -1})
	check("api: MaxDepth<0 报错", errDepth != nil)
	_, errLimit := api.Traverse(api.Request{Graph: g, Start: "A", Dir: api.Out, Limit: -1})
	check("api: Limit<0 报错", errLimit != nil)

	full, errFull := api.Traverse(api.Request{Graph: g, Start: "A", Dir: api.Out})
	check("api: 零值 MaxDepth/Limit 表示不限",
		errFull == nil && full.Err == nil && len(full.Nodes) == 5)

	resp, errLim := api.Traverse(api.Request{Graph: g, Start: "A", Dir: api.Out, Limit: 2})
	check("api: LimitCut 可经 api 哨兵判定",
		errLim == nil && errors.Is(resp.Err, api.ErrLimitCut) &&
			!errors.Is(resp.Err, api.ErrDepthCut))

	resp2, errDep := api.Traverse(api.Request{Graph: g, Start: "A", Dir: api.Out, MaxDepth: 1})
	check("api: DepthCut 可经 api 哨兵判定",
		errDep == nil && errors.Is(resp2.Err, api.ErrDepthCut) &&
			!errors.Is(resp2.Err, api.ErrLimitCut))
}
