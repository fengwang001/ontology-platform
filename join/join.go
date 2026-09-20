package join

import "sort"

// Join 对左右两表按 keys（有序）做等值连接，mode 为 Inner 或 Left。
//
// 语义总览（详细规则见包文档 doc.go）：
//   - 键为空（属性缺失 / nil / NaN）的行永不匹配，Left 模式下左行照常输出；
//   - 同键 m 行 × n 行完整展开为 m*n 行；
//   - 输出顺序与输入顺序、map 迭代顺序无关；
//   - 键类型冲突返回 *TypeError / *UnsupportedTypeError，而不是判为不等。
//
// 返回的每一行都是深拷贝，与输入行互不影响。
func Join(left, right []Row, keys []string, mode Mode) (*Result, error) {
	if len(keys) == 0 {
		return nil, ErrNoKeys
	}
	if mode != Inner && mode != Left {
		return nil, ErrInvalidMode
	}
	if err := validateKeyTypes(left, right, keys); err != nil {
		return nil, err
	}

	stats := Stats{LeftRows: len(left), RightRows: len(right)}

	// 右表按键建索引；键为空的右行只计数、不入索引。
	rightIndex := make(map[string][]int)
	rightIDs := make([]string, len(right))
	for j, r := range right {
		rightIDs[j] = RowID(r)
		vals, nullKey, err := extractKey(r, keys, "right")
		if err != nil {
			return nil, err
		}
		if nullKey {
			stats.RightNullKeyRows++
			continue
		}
		k := encodeKey(vals)
		rightIndex[k] = append(rightIndex[k], j)
	}

	var out []outRow
	leftKeyCount := make(map[string]int)
	for _, l := range left {
		vals, nullKey, err := extractKey(l, keys, "left")
		if err != nil {
			return nil, err
		}
		lid := RowID(l)
		if nullKey {
			stats.LeftNullKeyRows++
			if mode == Left {
				out = append(out, outRow{row: DeepCopyRow(l), leftID: lid})
			}
			continue
		}
		k := encodeKey(vals)
		leftKeyCount[k]++
		matches := rightIndex[k]
		if len(matches) == 0 {
			stats.LeftUnmatchedRows++
			if mode == Left {
				out = append(out, outRow{row: DeepCopyRow(l), leftID: lid})
			}
			continue
		}
		for _, j := range matches {
			out = append(out, outRow{
				row:     mergeRows(l, right[j], keys),
				key:     vals,
				leftID:  lid,
				rightID: rightIDs[j],
			})
		}
	}

	// 展开统计：逐键 m*n 求和，并记录最大单键展开倍数。
	for k, m := range leftKeyCount {
		exp := m * len(rightIndex[k])
		stats.MatchedRows += exp
		if exp > stats.MaxKeyExpansion {
			stats.MaxKeyExpansion = exp
		}
	}

	sort.SliceStable(out, func(a, b int) bool { return lessOutRow(out[a], out[b]) })

	rows := make([]Row, len(out))
	for i, o := range out {
		rows[i] = o.row
	}
	stats.OutputRows = len(rows)
	return &Result{Rows: rows, Stats: stats}, nil
}

// outRow 是携带排序键的中间结果行。
type outRow struct {
	row     Row
	key     []keyVal // nil 表示 Left 模式下的未匹配左行
	leftID  string
	rightID string
}

// lessOutRow 定义输出的全序：
// 匹配行在前，按连接键逐列升序，再按左行标识、右行标识升序；
// 未匹配左行排在最后，按左行标识升序。
func lessOutRow(a, b outRow) bool {
	am, bm := a.key != nil, b.key != nil
	if am != bm {
		return am
	}
	if am {
		for i := range a.key {
			if c := compareKeyVal(a.key[i], b.key[i]); c != 0 {
				return c < 0
			}
		}
	}
	if a.leftID != b.leftID {
		return a.leftID < b.leftID
	}
	return a.rightID < b.rightID
}

// mergeRows 构造匹配结果行：
//   - 左表属性保持原名；
//   - 右表的连接键属性不再重复出现（以左表为准）；
//   - 右表与左表同名的非连接键属性重命名为 "right." 前缀，
//     若仍冲突则继续叠加前缀（如 "right.right.x"），保证右表绝不覆盖左表；
//   - 右表其余属性保持原名。
func mergeRows(l, r Row, keys []string) Row {
	out := DeepCopyRow(l)
	for k, v := range r {
		if isJoinKey(k, keys) {
			continue
		}
		name := k
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

func isJoinKey(name string, keys []string) bool {
	for _, k := range keys {
		if k == name {
			return true
		}
	}
	return false
}

// Has 报告行中是否存在某属性。
// Left 模式下未匹配行的右侧属性一律缺失（而非零值），
// 调用方可用 Has 或 comma-ok 明确辨认。
func Has(r Row, name string) bool {
	_, ok := r[name]
	return ok
}
