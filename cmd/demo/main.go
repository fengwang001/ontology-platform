// demo 逐条打印遍历器语义判定的 OK/FAIL，任一 FAIL 以非零码退出。
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"

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
	check("skeleton runs", true)
	checkGraph()
	checkTraverse()
	checkBounded()
	checkAPI()

	if failures > 0 {
		fmt.Printf("exit=1 (%d failures)\n", failures)
		os.Exit(1)
	}
	fmt.Println("exit=0")
}

func checkAPI() {
	chain := chain5()
	cycle := cycle3()

	// 参数校验
	_, err := api.Traverse(api.Request{Graph: chain, Start: "ghost", Dir: traverse.Out})
	check("api: missing start rejected", errors.Is(err, api.ErrNoStart))
	_, err = api.Traverse(api.Request{Graph: chain, Start: "a", Dir: traverse.Dir(42)})
	check("api: invalid dir rejected", errors.Is(err, api.ErrInvalidDir))
	_, err = api.Traverse(api.Request{Start: "a", Dir: traverse.Out})
	check("api: nil graph rejected", errors.Is(err, api.ErrNilGraph))

	// 端到端三态
	res, err := api.Traverse(api.Request{Graph: chain, Start: "a", Dir: traverse.Out})
	check("api: unbounded traverse Complete", err == nil && res.Err == nil && len(res.Nodes) == 5)
	res, _ = api.Traverse(api.Request{Graph: chain, Start: "a", Dir: traverse.Out, Limit: 3})
	check("api: LimitCut via errors.Is",
		errors.Is(res.Err, api.ErrLimitCut) && !errors.Is(res.Err, api.ErrDepthCut))
	res, _ = api.Traverse(api.Request{Graph: chain, Start: "a", Dir: traverse.Out, MaxDepth: 2})
	check("api: DepthCut via errors.Is",
		errors.Is(res.Err, api.ErrDepthCut) && !errors.Is(res.Err, api.ErrLimitCut))
	res, _ = api.Traverse(api.Request{Graph: cycle, Start: "a", Dir: traverse.Out, MaxDepth: 2})
	check("api: cycle within depth is Complete", res.Err == nil && len(res.Nodes) == 3)

	// 环检测入口
	cyclic, _ := api.HasCycle(cycle)
	check("api: HasCycle true on cycle", cyclic)
	cyclic, _ = api.HasCycle(diamond())
	check("api: HasCycle false on diamond", !cyclic)
}

func checkBounded() {
	chain := chain5()
	cycle := cycle3()

	// limit 恰好 == 可达数 → Complete；少 1 → LimitCut
	res, _ := bounded.New(chain, traverse.Out, bounded.Limit(5)).Walk("a")
	check("bounded: limit exact is Complete", res.Err == nil && len(res.Nodes) == 5)
	res, _ = bounded.New(chain, traverse.Out, bounded.Limit(4)).Walk("a")
	check("bounded: limit below is LimitCut", errors.Is(res.Err, bounded.ErrLimitCut))
	// 环去重后恰好占满 limit → Complete；占不满 → LimitCut
	res, _ = bounded.New(cycle, traverse.Out, bounded.Limit(3)).Walk("a")
	check("bounded: cycle exact limit is Complete", res.Err == nil && len(res.Nodes) == 3)
	res, _ = bounded.New(cycle, traverse.Out, bounded.Limit(2)).Walk("a")
	check("bounded: cycle limit is LimitCut", errors.Is(res.Err, bounded.ErrLimitCut))

	// 深度：d 覆盖环周长 → Complete；d 更小 → DepthCut
	res, _ = bounded.New(cycle, traverse.Out, bounded.MaxDepth(2)).Walk("a")
	check("bounded: cycle depth sufficient is Complete", res.Err == nil && len(res.Nodes) == 3)
	res, _ = bounded.New(cycle, traverse.Out, bounded.MaxDepth(1)).Walk("a")
	check("bounded: cycle depth cut is DepthCut",
		errors.Is(res.Err, bounded.ErrDepthCut) && len(res.Nodes) == 2)
	res, _ = bounded.New(chain, traverse.Out, bounded.MaxDepth(2)).Walk("a")
	check("bounded: chain depth cut is DepthCut",
		errors.Is(res.Err, bounded.ErrDepthCut) && len(res.Nodes) == 3)

	// 三态互斥且可分别判定
	res, _ = bounded.New(chain, traverse.Out, bounded.Limit(4)).Walk("a")
	check("bounded: LimitCut is not DepthCut",
		errors.Is(res.Err, bounded.ErrLimitCut) && !errors.Is(res.Err, bounded.ErrDepthCut))
	res, _ = bounded.New(chain, traverse.Out, bounded.MaxDepth(2)).Walk("a")
	check("bounded: DepthCut is not LimitCut",
		errors.Is(res.Err, bounded.ErrDepthCut) && !errors.Is(res.Err, bounded.ErrLimitCut))

	// 不变量：输出 + 未展开 == 可达数（环上只贡献一次）
	w := bounded.New(cycle, traverse.Out, bounded.Limit(2))
	res, _ = w.Walk("a")
	output, unexpanded := w.Counters()
	check("bounded: output+unexpanded == reachable on cycle",
		output == len(res.Nodes) && output+unexpanded == 3)
}

func checkTraverse() {
	g := buildGraph(
		[]string{"a", "b", "c", "d", "e"},
		[][2]string{{"a", "b"}, {"a", "c"}, {"b", "d"}, {"c", "d"}, {"d", "a"}, {"d", "e"}},
	)
	cases := []struct {
		name  string
		start string
		dir   traverse.Dir
		want  []string
	}{
		{"out BFS dedups diamond+cycle", "a", traverse.Out, []string{"a", "b", "c", "d", "e"}},
		{"in walks upstream", "d", traverse.In, []string{"d", "b", "c", "a"}},
		{"both covers both sides", "b", traverse.Both, []string{"b", "a", "d", "c", "e"}},
	}
	for _, tc := range cases {
		got, err := traverse.Walk(g, tc.start, tc.dir)
		check("traverse: "+tc.name, err == nil && reflect.DeepEqual(got, tc.want))
	}
	_, err := traverse.Walk(g, "a", traverse.Dir(99))
	check("traverse: invalid dir rejected", err != nil)
	_, err = traverse.Walk(g, "missing", traverse.Out)
	check("traverse: missing start rejected", err != nil)
}

func buildGraph(nodes []string, edges [][2]string) *graph.Graph {
	g := graph.New()
	for _, n := range nodes {
		if err := g.AddNode(n); err != nil {
			check(fmt.Sprintf("graph add node %s", n), false)
		}
	}
	for _, e := range edges {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			check(fmt.Sprintf("graph add edge %s->%s", e[0], e[1]), false)
		}
	}
	return g
}

func chain5() *graph.Graph {
	return buildGraph([]string{"a", "b", "c", "d", "e"}, [][2]string{{"a", "b"}, {"b", "c"}, {"c", "d"}, {"d", "e"}})
}

func cycle3() *graph.Graph {
	return buildGraph([]string{"a", "b", "c"}, [][2]string{{"a", "b"}, {"b", "c"}, {"c", "a"}})
}

func diamond() *graph.Graph {
	return buildGraph([]string{"a", "b", "c", "d"}, [][2]string{{"a", "b"}, {"a", "c"}, {"b", "d"}, {"c", "d"}})
}

func checkGraph() {
	check("graph: diamond not reported as cycle", !diamond().HasCycle())

	check("graph: true cycle detected", cycle3().HasCycle())

	check("graph: self loop rejected",
		buildGraph([]string{"a"}, nil).AddEdge("a", "a") != nil)
	check("graph: duplicate edge rejected",
		buildGraph([]string{"a", "b"}, [][2]string{{"a", "b"}}).AddEdge("a", "b") != nil)

	for _, v := range []int{100, 10000} {
		nodes := make([]string, v)
		edges := make([][2]string, 0, v)
		for i := range nodes {
			nodes[i] = fmt.Sprintf("n%d", i)
			if i+1 < v {
				edges = append(edges, [2]string{fmt.Sprintf("n%d", i), fmt.Sprintf("n%d", i+1)})
			}
		}
		edges = append(edges, [2]string{nodes[v-1], nodes[0]})
		g := buildGraph(nodes, edges)
		n, e := g.Size()
		check(fmt.Sprintf("graph: HasCycle O(V+E) at V=%d", v),
			g.HasCycle() && g.Examined() <= n+e)
	}
}
