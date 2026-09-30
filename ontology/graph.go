package ontology

import (
	"fmt"
	"sort"
)

type partKey struct {
	asset string
	part  int
}

type edge struct {
	up   string
	down string
	lo   int
	hi   int
}

type asset struct {
	name  string
	first int
	last  int
	depth int
}

// consumed 给出下游分区 d 实际读取的该边上的上游分区（落在声明范围内）。
func (e *edge) consumed(up *asset, d int) []int {
	var out []int
	lo := d + e.lo
	if lo < up.first {
		lo = up.first
	}
	hi := d + e.hi
	if hi > up.last {
		hi = up.last
	}
	for p := lo; p <= hi; p++ {
		out = append(out, p)
	}
	return out
}

func (p *Planner) build(specs []AssetSpec, edgeSpecs []EdgeSpec) error {
	// 1) 资产声明：重复资产名（范围按规定次序稍后校验）。
	for _, s := range specs {
		if _, exists := p.assets[s.Name]; exists {
			return newError(ReasonDuplicateAsset,
				fmt.Sprintf("asset %q declared more than once", s.Name))
		}
		p.assets[s.Name] = &asset{name: s.Name, first: s.First, last: s.Last, depth: -1}
	}

	// 2) 边引用未知资产（按出现次序报告）。
	for _, e := range edgeSpecs {
		if _, ok := p.assets[e.Up]; !ok {
			return newError(ReasonUnknownAsset,
				fmt.Sprintf("edge references unknown upstream asset %q", e.Up))
		}
		if _, ok := p.assets[e.Down]; !ok {
			return newError(ReasonUnknownAsset,
				fmt.Sprintf("edge references unknown downstream asset %q", e.Down))
		}
	}

	// 3) 重复边（四元组完全相同）。
	seenEdges := map[[4]any]bool{}
	for _, e := range edgeSpecs {
		k := [4]any{e.Up, e.Down, e.Lo, e.Hi}
		if seenEdges[k] {
			return newError(ReasonDuplicateEdge,
				fmt.Sprintf("duplicate edge %s->%s [%d,%d]", e.Up, e.Down, e.Lo, e.Hi))
		}
		seenEdges[k] = true
	}

	// 4) 依赖成环（含自依赖）：先于偏移/范围校验。
	if cyc := findCycle(p.assets, adjFromEdges(edgeSpecs)); cyc != nil {
		err := newError(ReasonCycle, "dependency graph contains a cycle")
		err.Details["cycle"] = cyc
		return err
	}

	// 5) 偏移 lo > hi。
	for _, e := range edgeSpecs {
		if e.Lo > e.Hi {
			return newError(ReasonBadOffset,
				fmt.Sprintf("edge %s->%s has lo %d > hi %d", e.Up, e.Down, e.Lo, e.Hi))
		}
	}

	// 6) 资产范围 first > last。
	for _, s := range specs {
		if s.First > s.Last {
			return newError(ReasonBadRange,
				fmt.Sprintf("asset %q has first %d > last %d", s.Name, s.First, s.Last))
		}
	}

	// 7) 装配邻接并计算层深。
	adj := map[string][]string{}
	for _, e := range edgeSpecs {
		ed := &edge{up: e.Up, down: e.Down, lo: e.Lo, hi: e.Hi}
		p.inputs[e.Down] = append(p.inputs[e.Down], ed)
		adj[e.Down] = append(adj[e.Down], e.Up)
	}

	// 层深：源为 0，其余为上游层深最大值加一。
	for _, a := range p.assets {
		a.depth = p.computeDepth(a.name, adj, map[string]int{})
	}
	for _, a := range p.assets {
		p.byDepth = append(p.byDepth, a)
	}
	sort.Slice(p.byDepth, func(i, j int) bool {
		if p.byDepth[i].depth != p.byDepth[j].depth {
			return p.byDepth[i].depth < p.byDepth[j].depth
		}
		return p.byDepth[i].name < p.byDepth[j].name
	})

	p.logger.Printf("build ok: %d assets, %d edges", len(specs), len(edgeSpecs))
	return nil
}

func adjFromEdges(edgeSpecs []EdgeSpec) map[string][]string {
	adj := map[string][]string{}
	for _, e := range edgeSpecs {
		adj[e.Down] = append(adj[e.Down], e.Up)
	}
	return adj
}

func (p *Planner) computeDepth(name string, adj map[string][]string, memo map[string]int) int {
	if d, ok := memo[name]; ok {
		return d
	}
	ups := adj[name]
	if len(ups) == 0 {
		memo[name] = 0
		return 0
	}
	max := -1
	for _, u := range ups {
		if d := p.computeDepth(u, adj, memo); d > max {
			max = d
		}
	}
	d := max + 1
	memo[name] = d
	return d
}

// findCycle 用 DFS 三色标记返回任意一条环上的资产名（含自依赖）。
func findCycle(assets map[string]*asset, adj map[string][]string) []string {
	const white, gray, black = 0, 1, 2
	color := map[string]int{}
	names := make([]string, 0, len(assets))
	for n := range assets {
		names = append(names, n)
	}
	sort.Strings(names)

	var stack []string
	var dfs func(string) []string
	dfs = func(n string) []string {
		color[n] = gray
		stack = append(stack, n)
		ups := append([]string(nil), adj[n]...)
		sort.Strings(ups)
		for _, u := range ups {
			if color[u] == gray {
				idx := 0
				for stack[idx] != u {
					idx++
				}
				return append(append([]string(nil), stack[idx:]...), u)
			}
			if color[u] == white {
				if cyc := dfs(u); cyc != nil {
					return cyc
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[n] = black
		return nil
	}
	for _, n := range names {
		if color[n] == white {
			if cyc := dfs(n); cyc != nil {
				return cyc
			}
		}
	}
	return nil
}
