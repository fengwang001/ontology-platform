// Package naive 是跨类型最短路径查询的独立朴素实现，用作差分测试的
// 参照模型。它从图的只读快照出发，用深度优先搜索枚举所有满足类别约束
// 的简单路径，再按 (总代价, 类别序列, 对象序列) 的全序取最小者。
// 它与主引擎共享的仅有 ontology 包中的公共类型；匹配器、比较器与搜索
// 均为独立实现。
package naive

import (
	"fmt"
	"slices"

	"ontology"
)

// Solve 在快照 snap 上求解查询 q，语义与 (*ontology.Graph).Query 相同。
func Solve(snap ontology.Snapshot, q ontology.PathQuery) (ontology.PathResult, error) {
	rank := make(map[ontology.Category]int, len(snap.Categories))
	for i, c := range snap.Categories {
		rank[c] = i
	}

	// 1. 参数非法判定（先于一切图上搜索）。
	if len(q.Pattern) == 0 {
		return ontology.PathResult{}, fmt.Errorf("%w: constraint sequence is empty", ontology.ErrInvalidParams)
	}
	for i, e := range q.Pattern {
		if e.Star && i != 0 && i != len(q.Pattern)-1 {
			return ontology.PathResult{}, fmt.Errorf("%w: repeat marker at middle position %d", ontology.ErrInvalidParams, i)
		}
		if !e.Any {
			if _, ok := rank[e.Category]; !ok {
				return ontology.PathResult{}, fmt.Errorf("%w: unknown category %q", ontology.ErrInvalidParams, e.Category)
			}
		}
	}
	startInfo, ok := snap.Objects[q.Start]
	if !ok {
		return ontology.PathResult{}, fmt.Errorf("%w: start object %q not found", ontology.ErrInvalidParams, q.Start)
	}
	endInfo, ok := snap.Objects[q.End]
	if !ok {
		return ontology.PathResult{}, fmt.Errorf("%w: end object %q not found", ontology.ErrInvalidParams, q.End)
	}

	// 2. 起点或终点所在对象类型被禁止参与路径查询。
	if snap.ObjectTypes[startInfo.Type].ForbiddenInPathQuery || snap.ObjectTypes[endInfo.Type].ForbiddenInPathQuery {
		return ontology.PathResult{}, fmt.Errorf("%w: endpoint object type", ontology.ErrForbiddenObjectType)
	}

	// 3. 枚举所有满足约束的简单路径。
	s := &solver{snap: snap, q: q, rank: rank}
	return s.run(), nil
}

// edge 是邻接表中的一条有向边。
type edge struct {
	id   ontology.LinkID
	to   ontology.ObjectID
	cat  ontology.Category
	cost int64
}

// candidate 是一条满足类别约束的候选路径。
type candidate struct {
	cost  int64
	cats  []ontology.Category
	objs  []ontology.ObjectID
	links []ontology.LinkID
}

type solver struct {
	snap ontology.Snapshot
	q    ontology.PathQuery
	rank map[ontology.Category]int

	best       *candidate
	equivalent int // 与 best 全序相等的最优候选数量
}

func (s *solver) run() ontology.PathResult {
	// 起点与终点相同：唯一可能的候选是长度为零的路径。
	if s.q.Start == s.q.End {
		if matchPattern(s.q.Pattern, nil) {
			return ontology.PathResult{Found: true, Objects: []ontology.ObjectID{s.q.Start}}
		}
		return ontology.PathResult{}
	}

	adj := s.buildAdjacency()
	visited := map[ontology.ObjectID]bool{s.q.Start: true}
	s.dfs(adj, s.q.Start, visited, nil, nil, []ontology.ObjectID{s.q.Start}, 0)

	if s.best == nil {
		return ontology.PathResult{}
	}
	return ontology.PathResult{
		Found:      true,
		Equivalent: s.equivalent > 1,
		Cost:       s.best.cost,
		Objects:    s.best.objs,
		Links:      s.best.links,
		Categories: s.best.cats,
	}
}

// buildAdjacency 按权限可见性过滤链接并建立有向邻接表。
// 查询者缺少权限的链接类型在此被当作不存在。
func (s *solver) buildAdjacency() map[ontology.ObjectID][]edge {
	adj := make(map[ontology.ObjectID][]edge)
	for _, l := range s.snap.Links {
		lt := s.snap.LinkTypes[l.Type]
		if len(lt.RestrictedTo) > 0 && !slices.Contains(lt.RestrictedTo, s.q.Principal) {
			continue
		}
		adj[l.From] = append(adj[l.From], edge{id: l.ID, to: l.To, cat: lt.Category, cost: lt.Cost})
		if lt.Bidirectional && l.To != l.From {
			adj[l.To] = append(adj[l.To], edge{id: l.ID, to: l.From, cat: lt.Category, cost: lt.Cost})
		}
	}
	return adj
}

// dfs 枚举从当前对象出发的所有简单路径。
func (s *solver) dfs(adj map[ontology.ObjectID][]edge, u ontology.ObjectID, visited map[ontology.ObjectID]bool,
	links []ontology.LinkID, cats []ontology.Category, objs []ontology.ObjectID, cost int64) {
	if u == s.q.End {
		if matchPattern(s.q.Pattern, cats) {
			s.consider(&candidate{cost: cost, cats: cats, objs: objs, links: links})
		}
		// 终点不得作为中间节点。
		return
	}
	for _, e := range adj[u] {
		v := e.to
		if visited[v] {
			continue
		}
		// 检查顺序固定：先隔离/禁止（对象级），链接级权限已在建表时过滤。
		if v != s.q.End {
			vo := s.snap.Objects[v]
			if vo.Isolated {
				continue
			}
			if s.snap.ObjectTypes[vo.Type].ForbiddenInPathQuery {
				continue
			}
		}
		visited[v] = true
		s.dfs(adj, v, visited,
			append(slices.Clone(links), e.id),
			append(slices.Clone(cats), e.cat),
			append(slices.Clone(objs), v),
			cost+e.cost)
		visited[v] = false
	}
}

// consider 用全序 (代价, 类别序列, 对象序列) 维护最优候选与等价计数。
func (s *solver) consider(c *candidate) {
	if s.best == nil || lessCand(s.rank, c, s.best) {
		s.best = c
		s.equivalent = 1
		return
	}
	if equalCand(s.rank, c, s.best) {
		s.equivalent++
		// 等价组内以链接标识序列最小者为确定性代表。
		if lessLinks(c.links, s.best.links) {
			s.best = c
		}
	}
}

func lessCand(rank map[ontology.Category]int, a, b *candidate) bool {
	if a.cost != b.cost {
		return a.cost < b.cost
	}
	if c := compareCats(rank, a.cats, b.cats); c != 0 {
		return c < 0
	}
	return compareObjs(a.objs, b.objs) < 0
}

func equalCand(rank map[ontology.Category]int, a, b *candidate) bool {
	return a.cost == b.cost &&
		compareCats(rank, a.cats, b.cats) == 0 &&
		compareObjs(a.objs, b.objs) == 0
}

// compareCats 按类别全序做字典序比较；互为前缀时短者更小。
func compareCats(rank map[ontology.Category]int, a, b []ontology.Category) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if rank[a[i]] != rank[b[i]] {
			return rank[a[i]] - rank[b[i]]
		}
	}
	return len(a) - len(b)
}

// compareObjs 按对象标识做字典序比较；互为前缀时短者更小。
func compareObjs(a, b []ontology.ObjectID) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

func lessLinks(a, b []ontology.LinkID) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// matchPattern 用「枚举两端重复次数」的方式判定类别序列是否匹配约束，
// 与主引擎的自动机实现相互独立。
//
// 约束 e0 e1 ... e(n-1) 匹配序列 cats 当且仅当存在首元素重复次数 k0
// （带标记时 >= 0，否则恰为 1）与末元素重复次数 kn（带标记时 >= 0，否则
// 恰为 1），使前 k0 个匹配 e0、中间 n-2 个逐一匹配 e1..e(n-2)、末 kn 个
// 匹配 e(n-1)，且 k0 + (n-2) + kn == len(cats)。
func matchPattern(pattern []ontology.PatternElem, cats []ontology.Category) bool {
	n := len(pattern)
	m := len(cats)
	matchElem := func(e ontology.PatternElem, c ontology.Category) bool {
		return e.Any || e.Category == c
	}
	allMatch := func(e ontology.PatternElem, seg []ontology.Category) bool {
		for _, c := range seg {
			if !matchElem(e, c) {
				return false
			}
		}
		return true
	}

	if n == 1 {
		if m == 0 {
			return pattern[0].Star
		}
		if !pattern[0].Star && m != 1 {
			return false
		}
		return allMatch(pattern[0], cats)
	}

	minFirst := 1
	maxFirst := 1
	if pattern[0].Star {
		minFirst = 0
		maxFirst = m
	}
	minLast := 1
	if pattern[n-1].Star {
		minLast = 0
	}
	middle := n - 2
	for k0 := minFirst; k0 <= maxFirst && k0+middle+minLast <= m; k0++ {
		if !allMatch(pattern[0], cats[:k0]) {
			// 首元素段只能整体向右扩展，失配后更长的 k0 也不可能匹配。
			break
		}
		ok := true
		for i := 0; i < middle; i++ {
			if !matchElem(pattern[1+i], cats[k0+i]) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		rest := cats[k0+middle:]
		// 末元素不带标记时必须恰好出现一次。
		if !pattern[n-1].Star && len(rest) != 1 {
			continue
		}
		if allMatch(pattern[n-1], rest) {
			return true
		}
	}
	return false
}
