package ontology

import "sort"

// snapshot 是 Store 状态在某一时刻的不可变深拷贝，
// 使计数过程不与增删改并发互相干扰，也不别名内部状态。
type snapshot struct {
	// docs 固定按 docID 字节序存放。
	docIDs []string
	docs   []*document
	parent map[string]string
}

func (s *Store) takeSnapshot() snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	docIDs := make([]string, 0, len(s.docs))
	for id := range s.docs {
		docIDs = append(docIDs, id)
	}
	sort.Strings(docIDs)

	docs := make([]*document, len(docIDs))
	for i, id := range docIDs {
		src := s.docs[id]
		nodes := make(map[string]map[string]struct{}, len(src.nodes))
		for dim, set := range src.nodes {
			copied := make(map[string]struct{}, len(set))
			for node := range set {
				copied[node] = struct{}{}
			}
			nodes[dim] = copied
		}
		docs[i] = &document{nodes: nodes}
	}

	parent := make(map[string]string, len(s.parent))
	for child, p := range s.parent {
		parent[child] = p
	}
	return snapshot{docIDs: docIDs, docs: docs, parent: parent}
}

// activeDims 计算生效维度集合。
// 维度 d 生效当且仅当 d 没有 parent，或其 parent 生效且 parent 的已选集合非空。
// 依赖关系为每个 child 至多一个 parent 的森林，故按 parent 链判定。
func activeDims(snap snapshot, selected map[string]map[string]struct{}) map[string]bool {
	// 候选维度：任意文档出现过的维度、被声明依赖的维度、以及带已选值的维度。
	dims := make(map[string]struct{})
	for _, doc := range snap.docs {
		for dim := range doc.nodes {
			dims[dim] = struct{}{}
		}
	}
	for child, parent := range snap.parent {
		dims[child] = struct{}{}
		dims[parent] = struct{}{}
	}
	for dim := range selected {
		dims[dim] = struct{}{}
	}

	memo := make(map[string]bool, len(dims))
	var isActive func(dim string) bool
	isActive = func(dim string) bool {
		if v, ok := memo[dim]; ok {
			return v
		}
		parent, hasParent := snap.parent[dim]
		if !hasParent {
			memo[dim] = true
			return true
		}
		active := isActive(parent) && len(selected[parent]) > 0
		memo[dim] = active
		return active
	}

	active := make(map[string]bool, len(dims))
	for dim := range dims {
		if isActive(dim) {
			active[dim] = true
		}
	}
	return active
}

// matches 判定文档是否满足除 skipDim 外全部“生效且已选非空”的维度约束。
// skipDim 即计数时要忽略自身过滤的维度。
func matches(doc *document, active map[string]bool, selected map[string]map[string]struct{}, skipDim string) bool {
	for dim := range active {
		if dim == skipDim {
			continue
		}
		sel := selected[dim]
		if len(sel) == 0 {
			continue
		}
		nodes := doc.nodes[dim]
		hit := false
		for value := range sel {
			if _, ok := nodes[value]; ok {
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

// validateSelected 校验 Facets 的 selected：维度名非空、值均为合法路径。
// 按维度名与值字节序检查以保证拒绝原因可复现。
func validateSelected(selected map[string]map[string]struct{}) error {
	dims := make([]string, 0, len(selected))
	for dim := range selected {
		dims = append(dims, dim)
	}
	sort.Strings(dims)
	for _, dim := range dims {
		if dim == "" {
			return ErrInvalidArgument
		}
		values := make([]string, 0, len(selected[dim]))
		for value := range selected[dim] {
			values = append(values, value)
		}
		sort.Strings(values)
		for _, value := range values {
			if !isValidPath(value) {
				return ErrInvalidArgument
			}
		}
	}
	return nil
}

// Facets 按各生效维度的已选值集合计算多选过滤分面计数。
func (s *Store) Facets(selected map[string]map[string]struct{}, topN int) (FacetsResult, error) {
	if topN < minTopN || topN > maxTopN {
		return FacetsResult{}, ErrInvalidArgument
	}
	if err := validateSelected(selected); err != nil {
		return FacetsResult{}, err
	}

	snap := s.takeSnapshot()
	active := activeDims(snap, selected)

	// M：满足全部生效且非空已选维度约束的文档。
	total := 0
	for _, doc := range snap.docs {
		if matches(doc, active, selected, "") {
			total++
		}
	}

	dims := make([]string, 0, len(active))
	for dim := range active {
		dims = append(dims, dim)
	}
	sort.Strings(dims)

	result := FacetsResult{Total: total}
	for _, dim := range dims {
		// count(d,v)：忽略 dim 自身过滤后，节点集合含 v 的文档数。
		counts := make(map[string]int)
		for _, doc := range snap.docs {
			if !matches(doc, active, selected, dim) {
				continue
			}
			for node := range doc.nodes[dim] {
				counts[node]++
			}
		}

		// 候选值 = count > 0 的节点 ∪ 该维度已选值。
		candidates := make(map[string]int)
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
		sort.Slice(values, func(i, j int) bool {
			if candidates[values[i]] != candidates[values[j]] {
				return candidates[values[i]] > candidates[values[j]]
			}
			return values[i] < values[j]
		})

		limit := len(values)
		if topN < limit {
			limit = topN
		}
		items := make([]FacetItem, 0, limit)
		for _, value := range values[:limit] {
			_, isSelected := selected[dim][value]
			items = append(items, FacetItem{
				Value:    value,
				Count:    candidates[value],
				Selected: isSelected,
			})
		}
		result.Facets = append(result.Facets, FacetResult{Dimension: dim, Items: items})
	}

	return result, nil
}
