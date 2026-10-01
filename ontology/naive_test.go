package ontology

// naiveStore 是完全独立、按题面逐文档重新判定的朴素参照实现。
// 它不做任何增量索引，直接保存原始 attrs，并在每次 Facets 时：
//  1. 重新计算每篇文档的层级节点集合；
//  2. 沿 parent 链递归判定维度生效；
//  3. 对每个维度逐文档重算 Total 与 count(d,v)。
type naiveStore struct {
	docs   map[string]map[string][]string
	parent map[string]string
}

func newNaiveStore() *naiveStore {
	return &naiveStore{
		docs:   map[string]map[string][]string{},
		parent: map[string]string{},
	}
}

func naiveNodes(attrs map[string][]string) map[string]map[string]struct{} {
	return documentNodes(attrs)
}

func (n *naiveStore) active(dim string, selected map[string]map[string]struct{}, seen map[string]bool) bool {
	parent, ok := n.parent[dim]
	if !ok {
		return true
	}
	if seen[dim] {
		return false // 防御性：参照实现不应出现环，环会被 Link 拒绝
	}
	seen[dim] = true
	return n.active(parent, selected, seen) && len(selected[parent]) > 0
}

func naiveMatch(nodes map[string]map[string]struct{}, active map[string]bool,
	selected map[string]map[string]struct{}, skip string) bool {
	for dim := range active {
		if dim == skip {
			continue
		}
		sel := selected[dim]
		if len(sel) == 0 {
			continue
		}
		hit := false
		for value := range sel {
			if _, ok := nodes[dim][value]; ok {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

func (n *naiveStore) facets(selected map[string]map[string]struct{}, topN int) FacetsResult {
	// 收集全部候选维度。
	dimSet := map[string]struct{}{}
	for _, attrs := range n.docs {
		for dim := range attrs {
			dimSet[dim] = struct{}{}
		}
	}
	for child, parent := range n.parent {
		dimSet[child] = struct{}{}
		dimSet[parent] = struct{}{}
	}
	for dim := range selected {
		dimSet[dim] = struct{}{}
	}

	active := map[string]bool{}
	for dim := range dimSet {
		if n.active(dim, selected, map[string]bool{}) {
			active[dim] = true
		}
	}

	// 固定 docID 顺序。
	docIDs := make([]string, 0, len(n.docs))
	for id := range n.docs {
		docIDs = append(docIDs, id)
	}
	sortStrings(docIDs)

	type docEntry struct {
		id    string
		nodes map[string]map[string]struct{}
	}
	entries := make([]docEntry, len(docIDs))
	for i, id := range docIDs {
		entries[i] = docEntry{id: id, nodes: naiveNodes(n.docs[id])}
	}

	total := 0
	for _, e := range entries {
		if naiveMatch(e.nodes, active, selected, "") {
			total++
		}
	}

	dims := make([]string, 0, len(active))
	for dim := range active {
		dims = append(dims, dim)
	}
	sortStrings(dims)

	result := FacetsResult{Total: total}
	for _, dim := range dims {
		counts := map[string]int{}
		for _, e := range entries {
			if !naiveMatch(e.nodes, active, selected, dim) {
				continue
			}
			counted := map[string]struct{}{}
			for node := range e.nodes[dim] {
				if _, ok := counted[node]; ok {
					continue
				}
				counted[node] = struct{}{}
				counts[node]++
			}
		}
		candidates := map[string]int{}
		for node, count := range counts {
			if count > 0 {
				candidates[node] = count
			}
		}
		for value := range selected[dim] {
			if _, ok := candidates[value]; !ok {
				candidates[value] = 0
			}
		}
		if len(candidates) == 0 {
			continue
		}
		values := make([]string, 0, len(candidates))
		for value := range candidates {
			values = append(values, value)
		}
		sortStringsByCount(values, candidates)
		limit := len(values)
		if topN < limit {
			limit = topN
		}
		items := make([]FacetItem, 0, limit)
		for _, value := range values[:limit] {
			_, isSelected := selected[dim][value]
			items = append(items, FacetItem{Value: value, Count: candidates[value], Selected: isSelected})
		}
		result.Facets = append(result.Facets, FacetResult{Dimension: dim, Items: items})
	}
	return result
}
