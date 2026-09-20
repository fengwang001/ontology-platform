package join

// Row 是表中的一行：属性名到值的映射。
type Row = map[string]any

// Mode 是连接模式。
type Mode int

const (
	// Inner 只输出左右两侧键相等的行对。
	Inner Mode = iota
	// Left 在 Inner 的基础上，额外输出所有未匹配的左行（右半侧属性缺失）。
	Left
)

// Stats 是本次连接的统计信息，Inner 与 Left 模式都会填充。
type Stats struct {
	// OutputRows 是本次连接产出的总行数（len(Result.Rows)）。
	OutputRows int
	// MatchedPairs 是键相等的 (左行, 右行) 行对总数，等于逐键 m*n 之和。
	MatchedPairs int
	// MaxKeyExpansion 是单个连接键展开出的最大行数（该键的 m*n），无匹配时为 0。
	MaxKeyExpansion int
	// LeftUnmatchedNull 是因连接键为空（缺失、nil 或 NaN）而未匹配的左行数。
	LeftUnmatchedNull int
	// LeftUnmatchedNoMatch 是键有值但右表无对应行而未匹配的左行数。
	LeftUnmatchedNoMatch int
}

// Result 是一次连接的结果。
type Result struct {
	Rows  []Row
	Stats Stats
}

// pair 记录一对匹配的左右行下标及其规范键。
type pair struct {
	vals  []keyVal
	left  int
	right int
}

// Join 对 left 与 right 按 keys 做等值连接。结果与输入互不影响；
// 输出顺序确定，详见包文档。键类型不可比时返回 *KeyTypeError。
func Join(left, right []Row, keys []string, mode Mode) (*Result, error) {
	if len(keys) == 0 {
		return nil, ErrNoKeys
	}
	lk := extractKeys(left, keys)
	rk := extractKeys(right, keys)
	if err := validateKeyTypes(keys, lk, rk); err != nil {
		return nil, err
	}

	rightIndex := make(map[canonKey][]int, len(right))
	for j, k := range rk.canon {
		if !rk.null[j] {
			rightIndex[k] = append(rightIndex[k], j)
		}
	}
	leftCount := make(map[canonKey]int, len(left))
	for i, k := range lk.canon {
		if !lk.null[i] {
			leftCount[k]++
		}
	}

	var stats Stats
	var pairs []pair
	var unmatched []int
	for i, k := range lk.canon {
		if lk.null[i] {
			stats.LeftUnmatchedNull++
			unmatched = append(unmatched, i)
			continue
		}
		rs := rightIndex[k]
		if len(rs) == 0 {
			stats.LeftUnmatchedNoMatch++
			unmatched = append(unmatched, i)
			continue
		}
		for _, j := range rs {
			pairs = append(pairs, pair{vals: lk.cols[i], left: i, right: j})
		}
	}

	stats.MatchedPairs = len(pairs)
	for k, m := range leftCount {
		n := len(rightIndex[k])
		if n > 0 && m*n > stats.MaxKeyExpansion {
			stats.MaxKeyExpansion = m * n
		}
	}

	leftIDs := make([]string, len(left))
	for i := range left {
		leftIDs[i] = RowID(left[i])
	}
	rightIDs := make([]string, len(right))
	for j := range right {
		rightIDs[j] = RowID(right[j])
	}
	sortPairs(pairs, leftIDs, rightIDs)

	keySet := make(map[string]bool, len(keys))
	for _, k := range keys {
		keySet[k] = true
	}
	rows := make([]Row, 0, len(pairs))
	for _, p := range pairs {
		rows = append(rows, buildRow(left[p.left], right[p.right], keySet))
	}
	if mode == Left {
		sortUnmatched(unmatched, leftIDs)
		for _, i := range unmatched {
			rows = append(rows, buildRow(left[i], nil, keySet))
		}
	}
	stats.OutputRows = len(rows)
	return &Result{Rows: rows, Stats: stats}, nil
}

// sideKeys 缓存一张表所有行的规范键。
type sideKeys struct {
	canon []canonKey
	cols  [][]keyVal
	null  []bool
}

func extractKeys(rows []Row, keys []string) sideKeys {
	sk := sideKeys{
		canon: make([]canonKey, len(rows)),
		cols:  make([][]keyVal, len(rows)),
		null:  make([]bool, len(rows)),
	}
	for i, row := range rows {
		sk.canon[i], sk.cols[i], sk.null[i] = keyOf(row, keys)
	}
	return sk
}

// buildRow 构造一行结果：左行深拷贝 + 右行非键属性加 "right." 前缀。
// r 为 nil 时表示 Left 模式的未匹配行，右半侧属性整体缺失。
func buildRow(l, r Row, keySet map[string]bool) Row {
	out := deepCopyRow(l)
	if r == nil {
		return out
	}
	for k, v := range r {
		if keySet[k] {
			continue
		}
		name := "right." + k
		for {
			if _, taken := out[name]; !taken {
				break
			}
			name = "right." + name
		}
		out[name] = deepCopyValue(v)
	}
	return out
}
