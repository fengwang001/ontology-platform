package ontology

import "sort"

// Mode 是连接模式。
type Mode int

const (
	// Inner 只输出左右两表都匹配上的行对。
	Inner Mode = iota
	// Left 在 Inner 的基础上，为每个未匹配的左行补一行（右侧属性缺失）。
	Left
)

// RightPrefix 是右侧非连接键属性在结果行中的统一前缀。
// 结果行中：连接键与左侧属性保持原名；右侧非键属性一律命名为
// "right."+原名，因此右表永远不会覆盖左表的同名属性。
const RightPrefix = "right."

// RightValue 读取结果行中名为 name 的右侧属性。
// 第二个返回值是"该右侧属性是否存在"：Left 模式下未匹配的左行，
// 其全部右侧属性都缺失（不是零值），调用方借此明确辨认。
func RightValue(row map[string]any, name string) (any, bool) {
	v, ok := row[RightPrefix+name]
	return v, ok
}

// group 是落在同一个连接键上的左右行下标集合。
type group struct {
	key    rowKey
	lefts  []int
	rights []int
}

// Join 对左右两表按 keys（有序）做等值连接，返回结果行与统计。
//
// 输出顺序完全确定，与两表输入顺序及 map 迭代顺序无关：
// 先按连接键逐列升序；同键内按"行标识"升序，先行左后行右。
// 行标识定义为：把该表全部行按 encodeRow（属性名排序后的内容编码）
// 升序排列后，该行所处的位次——完全相同的两行位次相邻，此时它们
// 产出的结果行也完全相同，故相对顺序不影响结果序列。
// Left 模式下，键为空的左行排在所有非空键分组之后，按行标识升序。
func Join(left, right []map[string]any, keys []string, mode Mode) ([]map[string]any, *Stats, error) {
	stats := &Stats{expansions: map[string]int{}}
	if err := checkKeyTypes(left, right, keys); err != nil {
		return nil, nil, err
	}

	leftRank := rowRanks(left)
	rightRank := rowRanks(right)

	groups := map[string]*group{}
	var nullLefts []int
	for i, row := range left {
		k := extractKey(row, keys)
		if k.null {
			nullLefts = append(nullLefts, i)
			continue
		}
		g := groups[k.encoded]
		if g == nil {
			g = &group{key: k}
			groups[k.encoded] = g
		}
		g.lefts = append(g.lefts, i)
	}
	for j, row := range right {
		k := extractKey(row, keys)
		if k.null {
			stats.RightUnmatchedRows++
			continue
		}
		g := groups[k.encoded]
		if g == nil {
			g = &group{key: k}
			groups[k.encoded] = g
		}
		g.rights = append(g.rights, j)
	}

	ordered := make([]*group, 0, len(groups))
	for _, g := range groups {
		ordered = append(ordered, g)
	}
	sort.Slice(ordered, func(a, b int) bool {
		return compareRowKeys(ordered[a].key, ordered[b].key) < 0
	})

	keySet := make(map[string]bool, len(keys))
	for _, k := range keys {
		keySet[k] = true
	}

	var out []map[string]any
	for _, g := range ordered {
		sort.Slice(g.lefts, func(a, b int) bool { return leftRank[g.lefts[a]] < leftRank[g.lefts[b]] })
		sort.Slice(g.rights, func(a, b int) bool { return rightRank[g.rights[a]] < rightRank[g.rights[b]] })
		if len(g.lefts) == 0 {
			stats.RightUnmatchedRows += len(g.rights)
			continue
		}
		if len(g.rights) > 0 {
			mn := len(g.lefts) * len(g.rights)
			stats.MatchedPairs += mn
			stats.expansions[g.key.encoded] = mn
			if mn > stats.MaxKeyExpansion {
				stats.MaxKeyExpansion = mn
			}
		}
		for _, i := range g.lefts {
			if len(g.rights) == 0 {
				stats.LeftUnmatchedRows++
				if mode == Left {
					out = append(out, buildRow(left[i], nil, keySet))
				}
				continue
			}
			for _, j := range g.rights {
				out = append(out, buildRow(left[i], right[j], keySet))
			}
		}
	}

	stats.LeftNullKeyRows = len(nullLefts)
	if mode == Left {
		sort.Slice(nullLefts, func(a, b int) bool { return leftRank[nullLefts[a]] < leftRank[nullLefts[b]] })
		for _, i := range nullLefts {
			out = append(out, buildRow(left[i], nil, keySet))
		}
	}
	stats.OutputRows = len(out)
	return out, stats, nil
}

// rowRanks 返回每行的行标识：按内容编码升序排列后的位次。
func rowRanks(rows []map[string]any) []int {
	order := make([]int, len(rows))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return encodeRow(rows[order[a]]) < encodeRow(rows[order[b]])
	})
	rank := make([]int, len(rows))
	for pos, idx := range order {
		rank[idx] = pos
	}
	return rank
}

// buildRow 构造结果行：左侧属性（含连接键）保持原名，右侧非键属性
// 统一加 RightPrefix。所有值深拷贝，结果与输入互不影响。
// rightRow 为 nil 表示 Left 模式的未匹配左行，右侧属性整体缺失。
func buildRow(leftRow, rightRow map[string]any, keySet map[string]bool) map[string]any {
	out := make(map[string]any, len(leftRow)+len(rightRow))
	for name, v := range leftRow {
		out[name] = deepCopyValue(v)
	}
	for name, v := range rightRow {
		if keySet[name] {
			continue
		}
		out[RightPrefix+name] = deepCopyValue(v)
	}
	return out
}
