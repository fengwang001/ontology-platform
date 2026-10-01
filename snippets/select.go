package snippets

import "sort"

// Snippets 在指定文档上按固定窗口贪心选取不重叠片段。
//
// 拒绝顺序（被拒绝不改变登记表）：docID 未登记 → ErrDocumentNotFound；
// W/K 越界或去重前 hits 超过 10000 个 → ErrInvalidArgument；
// 任一命中越界、s>=e 或端点不在字符边界 → ErrInvalidArgument。
// hits 为空合法，返回非 nil 的空切片与 nil 错误。
//
// 返回的 Snippet 及其 Highlights 均为新建切片，不别名内部状态。
func (r *Registry) Snippets(docID string, hits []Hit, W, K int) ([]Snippet, error) {
	r.mu.RLock()
	doc, ok := r.docs[docID]
	r.mu.RUnlock()
	if !ok {
		return nil, ErrDocumentNotFound
	}

	if W < 1 || W > 4096 || K < 1 || K > 16 || len(hits) > MaxHits {
		return nil, ErrInvalidArgument
	}

	uniq := dedupHits(hits)
	n := len(doc.text)
	for _, h := range uniq {
		if h.Start < 0 || h.End > n || h.Start >= h.End ||
			!doc.boundary[h.Start] || !doc.boundary[h.End] {
			return nil, ErrInvalidArgument
		}
	}

	return selectSnippets(uniq, n, W, K), nil
}

// dedupHits 按完全相同的 (Start, End) 去重，返回按 Start、End 升序排列的命中。
// 结果顺序与输入顺序无关。
func dedupHits(hits []Hit) []Hit {
	uniq := make([]Hit, 0, len(hits))
	seen := make(map[Hit]struct{}, len(hits))
	for _, h := range hits {
		if _, dup := seen[h]; dup {
			continue
		}
		seen[h] = struct{}{}
		uniq = append(uniq, h)
	}
	sort.Slice(uniq, func(i, j int) bool {
		if uniq[i].Start != uniq[j].Start {
			return uniq[i].Start < uniq[j].Start
		}
		return uniq[i].End < uniq[j].End
	})
	return uniq
}

// chosenRange 是已选片段 [start, end)。
type chosenRange struct {
	start int
	end   int
}

// selectSnippets 按规则逐轮贪心选取片段。
// hits 必须已去重且通过全部合法性检查；排序与否不影响正确性。
func selectSnippets(hits []Hit, n, W, K int) []Snippet {
	selected := make([]chosenRange, 0, K)
	// 每轮的 C(a)，按轮次保存，供输出高亮与分值使用。
	roundCovered := make([][]Hit, 0, K)

	for len(selected) < K {
		available := make([]Hit, 0, len(hits))
		for _, h := range hits {
			if intersectsAny(h, selected) {
				continue
			}
			available = append(available, h)
		}
		if len(available) == 0 {
			break
		}

		bestA, bestE, bestScore := evaluateRound(available, selected, n, W)

		if bestScore == 0 {
			break
		}
		bestCovered := make([]Hit, 0, bestScore)
		r := windowRight(bestA, n, W, selected)
		for _, h := range available {
			if h.Start >= bestA && h.End <= r {
				bestCovered = append(bestCovered, h)
			}
		}
		selected = append(selected, chosenRange{start: bestA, end: bestE})
		roundCovered = append(roundCovered, bestCovered)
	}

	order := make([]int, len(selected))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool {
		return selected[order[i]].start < selected[order[j]].start
	})

	out := make([]Snippet, 0, len(selected))
	for _, idx := range order {
		s := selected[idx]
		// 复制并排序该轮覆盖命中，确保返回内容不别名内部状态。
		covered := append([]Hit(nil), roundCovered[idx]...)
		sort.Slice(covered, func(i, j int) bool {
			if covered[i].Start != covered[j].Start {
				return covered[i].Start < covered[j].Start
			}
			return covered[i].End < covered[j].End
		})
		out = append(out, Snippet{
			Start:      s.start,
			End:        s.end,
			Highlights: mergeHighlights(covered),
			Score:      len(covered),
		})
	}
	return out
}

// evaluateRound 在一轮中选出 (a, E, |C(a)|)。
//
// 可用命中按起点降序扫入两个以 e 为坐标的树状数组：评估起点 a 时，
// 树中恰好包含全部 s>=a 的命中，于是计数树在 e<=r(a) 上的前缀和就是
// |C(a)|，最大值树给出 E。降序遍历中后处理的 a 更小，分值并列时用
// “>= 即替换”最终保留最小 a。整体每轮 O(H log H)。
func evaluateRound(available []Hit, selected []chosenRange, n, W int) (int, int, int) {
	byStart := groupByStart(available)
	starts := make([]int, 0, len(byStart))
	for a := range byStart {
		starts = append(starts, a)
	}
	sort.Ints(starts)

	coord := make([]int, 0, len(available))
	for _, h := range available {
		coord = append(coord, h.End)
	}
	sort.Ints(coord)
	coord = compact(coord)

	countTree := newFenwickCount(len(coord))
	maxTree := newFenwickMax(len(coord))
	bestA, bestE, bestScore := -1, -1, 0
	for i := len(starts) - 1; i >= 0; i-- {
		a := starts[i]
		for _, h := range byStart[a] {
			idx := sort.SearchInts(coord, h.End) + 1
			countTree.add(idx, 1)
			maxTree.update(idx, h.End)
		}
		r := windowRight(a, n, W, selected)
		cut := sort.SearchInts(coord, r+1)
		score := countTree.sum(cut)
		// 降序遍历：相等时替换为当前（更小的）a。
		if score >= bestScore {
			bestScore = score
			bestA = a
			bestE = maxTree.query(cut)
		}
	}
	return bestA, bestE, bestScore
}

// windowRight 计算 r(a) = min(a+W, n, 起点大于 a 的已选片段最小起点)。
func windowRight(a, n, W int, selected []chosenRange) int {
	r := a + W
	if r > n {
		r = n
	}
	for _, s := range selected {
		if s.start > a && s.start < r {
			r = s.start
		}
	}
	return r
}

// groupByStart 将命中按起点分组，map 的键即不同起点。
func groupByStart(hits []Hit) map[int][]Hit {
	m := make(map[int][]Hit)
	for _, h := range hits {
		m[h.Start] = append(m[h.Start], h)
	}
	return m
}

func compact(xs []int) []int {
	if len(xs) == 0 {
		return xs
	}
	out := xs[:1]
	for _, x := range xs[1:] {
		if x != out[len(out)-1] {
			out = append(out, x)
		}
	}
	return out
}

// fenwickCount 是点加、前缀和树状数组。
type fenwickCount struct{ tree []int }

func newFenwickCount(size int) *fenwickCount { return &fenwickCount{tree: make([]int, size+1)} }

func (f *fenwickCount) add(i, delta int) {
	for ; i < len(f.tree); i += i & -i {
		f.tree[i] += delta
	}
}

func (f *fenwickCount) sum(i int) int {
	total := 0
	for ; i > 0; i -= i & -i {
		total += f.tree[i]
	}
	return total
}

// fenwickMax 是点更新、前缀最大值树状数组。
type fenwickMax struct{ tree []int }

func newFenwickMax(size int) *fenwickMax {
	tree := make([]int, size+1)
	for i := range tree {
		tree[i] = -1
	}
	return &fenwickMax{tree: tree}
}

func (f *fenwickMax) update(i, val int) {
	for ; i < len(f.tree); i += i & -i {
		if val > f.tree[i] {
			f.tree[i] = val
		}
	}
}

func (f *fenwickMax) query(i int) int {
	best := -1
	for ; i > 0; i -= i & -i {
		if f.tree[i] > best {
			best = f.tree[i]
		}
	}
	return best
}

// intersectsAny 报告命中 h 是否与任一已选片段相交（半开区间相交判定）。
func intersectsAny(h Hit, selected []chosenRange) bool {
	for _, s := range selected {
		if h.Start < s.end && s.start < h.End {
			return true
		}
	}
	return false
}

// mergeHighlights 将命中按起点升序合并重叠者：仅当 s2 < e1 时合并，
// 相接（s2 == e1）不合并。
func mergeHighlights(hs []Hit) []Range {
	if len(hs) == 0 {
		return []Range{}
	}
	sorted := append([]Hit(nil), hs...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Start != sorted[j].Start {
			return sorted[i].Start < sorted[j].Start
		}
		return sorted[i].End < sorted[j].End
	})
	result := make([]Range, 0, len(sorted))
	cur := Range{Start: sorted[0].Start, End: sorted[0].End}
	for _, h := range sorted[1:] {
		if h.Start < cur.End {
			if h.End > cur.End {
				cur.End = h.End
			}
		} else {
			result = append(result, cur)
			cur = Range{Start: h.Start, End: h.End}
		}
	}
	result = append(result, cur)
	return result
}
