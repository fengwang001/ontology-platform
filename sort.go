package ontology

import (
	"fmt"
	"sort"
)

// SortKey 描述一个排序键：字段名、是否降序、空值是否排前。
// 降序只作用于键值比较结果，绝不影响相等行的相对顺序；
// 空值位置只由 NullsFirst 决定，与 Desc 正交。
type SortKey struct {
	Field      string
	Desc       bool
	NullsFirst bool
}

// Row 是排序结果中的一行，携带它在输入切片中的原始下标。
type Row struct {
	Index int
	Data  map[string]any
}

// Stats 是单次排序调用的统计信息，每次调用独立计数，并发互不串台。
type Stats struct {
	// Comparisons 是排序过程中评估排序键的总次数（按"每对行每考察一个键"计 1）。
	Comparisons int
	// NaNValues 是排序过程中遇到的 NaN 键值个数（NaN 按空值对待）。
	NaNValues int
}

// Result 是一次排序的输出。
type Result struct {
	Rows  []Row
	Stats Stats
}

// Indices 返回结果中各行在输入切片中的原始下标，便于直接验证稳定性。
func (r *Result) Indices() []int {
	out := make([]int, len(r.Rows))
	for i, row := range r.Rows {
		out[i] = row.Index
	}
	return out
}

// Sorter 按一组有序排序键对行做稳定排序。Sorter 本身不可变，可并发使用。
type Sorter struct {
	keys []SortKey
}

// NewSorter 构造排序器；keys 按优先级从高到低排列。
func NewSorter(keys ...SortKey) *Sorter {
	k := make([]SortKey, len(keys))
	copy(k, keys)
	return &Sorter{keys: k}
}

// sortState 是单次调用的可变状态，绝不共享。
type sortState struct {
	stats Stats
	err   error
}

// Sort 对 rows 做稳定排序，返回新切片；不修改传入的行，也不修改传入切片的顺序。
func (s *Sorter) Sort(rows []map[string]any) (*Result, error) {
	indexed := make([]Row, len(rows))
	for i, r := range rows {
		indexed[i] = Row{Index: i, Data: r}
	}
	st := &sortState{}
	sort.SliceStable(indexed, func(i, j int) bool {
		if st.err != nil {
			return false
		}
		cmp, err := s.compareRows(indexed[i].Data, indexed[j].Data, st)
		if err != nil {
			st.err = err
			return false
		}
		return cmp < 0
	})
	if st.err != nil {
		return nil, st.err
	}
	return &Result{Rows: indexed, Stats: st.stats}, nil
}

// compareRows 依次考察各排序键，一旦分出胜负立即短路返回。
func (s *Sorter) compareRows(a, b map[string]any, st *sortState) (int, error) {
	for _, k := range s.keys {
		st.stats.Comparisons++
		cmp, err := compareKey(k, a, b, st)
		if err != nil {
			return 0, err
		}
		if cmp != 0 {
			return cmp, nil
		}
	}
	return 0, nil
}

// compareKey 在单个键上比较两行：先按空值规则，再按键值，最后按 Desc 取反。
func compareKey(k SortKey, a, b map[string]any, st *sortState) (int, error) {
	av, aNull := keyValue(a, k.Field, st)
	bv, bNull := keyValue(b, k.Field, st)
	switch {
	case aNull && bNull:
		return 0, nil
	case aNull:
		if k.NullsFirst {
			return -1, nil
		}
		return 1, nil
	case bNull:
		if k.NullsFirst {
			return 1, nil
		}
		return -1, nil
	}
	cmp, ok := compareValues(av, bv)
	if !ok {
		return 0, &TypeMismatchError{
			Key:   k.Field,
			TypeA: fmt.Sprintf("%T", av),
			TypeB: fmt.Sprintf("%T", bv),
		}
	}
	if k.Desc {
		cmp = -cmp
	}
	return cmp, nil
}

// keyValue 取字段值并判定是否按空值对待（缺失、nil、NaN）。
func keyValue(row map[string]any, field string, st *sortState) (any, bool) {
	switch Classify(row, field) {
	case NullMissing, NullNil:
		return nil, true
	case NullNaN:
		st.stats.NaNValues++
		return nil, true
	default:
		return row[field], false
	}
}
