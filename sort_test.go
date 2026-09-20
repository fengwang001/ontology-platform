package ontology

import (
	"reflect"
	"testing"
)

func allEqualRows(n int) []map[string]any {
	rows := make([]map[string]any, n)
	for i := range rows {
		rows[i] = map[string]any{"k": int64(7), "tag": i}
	}
	return rows
}

func resultTags(t *testing.T, res *Result) []int {
	t.Helper()
	tags := make([]int, len(res.Rows))
	for i, row := range res.Rows {
		tags[i] = row.Data["tag"].(int)
	}
	return tags
}

// 全键相等的行，输出必须保持输入下标严格递增。
func TestStableAllKeysEqual(t *testing.T) {
	rows := allEqualRows(50)
	s := NewSorter(SortKey{Field: "k"})
	res, err := s.Sort(rows)
	if err != nil {
		t.Fatalf("Sort: %v", err)
	}
	for i, row := range res.Rows {
		if row.Index != i {
			t.Fatalf("position %d holds input index %d, want %d", i, row.Index, i)
		}
	}
}

// 降序只作用于键值：键值相等的行，升序与降序下相对顺序完全一致。
func TestDescDoesNotFlipEqualRows(t *testing.T) {
	asc := NewSorter(SortKey{Field: "k"})
	desc := NewSorter(SortKey{Field: "k", Desc: true})
	resAsc, err := asc.Sort(allEqualRows(30))
	if err != nil {
		t.Fatalf("Sort asc: %v", err)
	}
	resDesc, err := desc.Sort(allEqualRows(30))
	if err != nil {
		t.Fatalf("Sort desc: %v", err)
	}
	if !reflect.DeepEqual(resultTags(t, resAsc), resultTags(t, resDesc)) {
		t.Fatalf("desc flipped equal rows: asc=%v desc=%v",
			resultTags(t, resAsc), resultTags(t, resDesc))
	}
}

// 排序不得修改传入的行，也不得修改传入切片的顺序。
func TestSortDoesNotMutateInput(t *testing.T) {
	rows := []map[string]any{
		{"k": int64(3), "s": "c"},
		{"k": int64(1), "s": "a"},
		{"k": int64(2), "s": "b"},
	}
	wantRows := make([]map[string]any, len(rows))
	for i, r := range rows {
		cp := make(map[string]any, len(r))
		for k, v := range r {
			cp[k] = v
		}
		wantRows[i] = cp
	}
	s := NewSorter(SortKey{Field: "k"})
	res, err := s.Sort(rows)
	if err != nil {
		t.Fatalf("Sort: %v", err)
	}
	if !reflect.DeepEqual(rows, wantRows) {
		t.Fatalf("input mutated: got %v want %v", rows, wantRows)
	}
	gotIdx := res.Indices()
	if !reflect.DeepEqual(gotIdx, []int{1, 2, 0}) {
		t.Fatalf("indices = %v, want [1 2 0]", gotIdx)
	}
}

// 多键排序：第一键分不出胜负时由第二键决定。
func TestMultiKeyOrdering(t *testing.T) {
	rows := []map[string]any{
		{"a": int64(1), "b": "x"},
		{"a": int64(2), "b": "y"},
		{"a": int64(1), "b": "w"},
	}
	s := NewSorter(SortKey{Field: "a"}, SortKey{Field: "b", Desc: true})
	res, err := s.Sort(rows)
	if err != nil {
		t.Fatalf("Sort: %v", err)
	}
	if got := res.Indices(); !reflect.DeepEqual(got, []int{0, 2, 1}) {
		t.Fatalf("indices = %v, want [0 2 1]", got)
	}
}
