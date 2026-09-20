package ontology

import (
	"errors"
	"reflect"
	"testing"
)

// 同一键上 string 与 int64 混排：返回可判定错误，带键名与两种类型。
func TestTypeMismatchError(t *testing.T) {
	rows := []map[string]any{
		{"k": "apple"},
		{"k": int64(3)},
		{"k": "banana"},
	}
	_, err := NewSorter(SortKey{Field: "k"}).Sort(rows)
	if err == nil {
		t.Fatal("Sort succeeded, want TypeMismatchError")
	}
	var tm *TypeMismatchError
	if !errors.As(err, &tm) {
		t.Fatalf("error type = %T, want *TypeMismatchError", err)
	}
	if tm.Key != "k" {
		t.Fatalf("Key = %q, want %q", tm.Key, "k")
	}
	pair := map[string]bool{tm.TypeA: true, tm.TypeB: true}
	if !pair["string"] || !pair["int64"] {
		t.Fatalf("types = %q/%q, want string/int64", tm.TypeA, tm.TypeB)
	}
}

// int64 与 float64 按数值比较，而不是按类型分组。
func TestInt64Float64NumericCompare(t *testing.T) {
	rows := []map[string]any{
		{"k": int64(2)},
		{"k": 1.5},
		{"k": int64(1)},
		{"k": 2.5},
	}
	res, err := NewSorter(SortKey{Field: "k"}).Sort(rows)
	if err != nil {
		t.Fatalf("Sort: %v", err)
	}
	if got := res.Indices(); !reflect.DeepEqual(got, []int{2, 1, 0, 3}) {
		t.Fatalf("indices = %v, want [2 1 0 3]", got)
	}
}

// 大 int64 与 float64 的比较不能因 float64 精度损失而出错。
func TestInt64Float64Precision(t *testing.T) {
	big := int64(1<<53 + 1)
	rows := []map[string]any{
		{"k": float64(1 << 53)},
		{"k": big},
	}
	res, err := NewSorter(SortKey{Field: "k"}).Sort(rows)
	if err != nil {
		t.Fatalf("Sort: %v", err)
	}
	if got := res.Indices(); !reflect.DeepEqual(got, []int{0, 1}) {
		t.Fatalf("indices = %v, want [0 1]", got)
	}
}

// 第一键已分出胜负时，后续键不再比较：比较次数不随键个数增长。
func TestShortCircuitComparisonCount(t *testing.T) {
	rows := []map[string]any{
		{"a": int64(5), "b": "x", "c": "x"},
		{"a": int64(1), "b": "y", "c": "y"},
		{"a": int64(9), "b": "z", "c": "z"},
		{"a": int64(3), "b": "w", "c": "w"},
	}
	one := NewSorter(SortKey{Field: "a"})
	three := NewSorter(
		SortKey{Field: "a"},
		SortKey{Field: "b"},
		SortKey{Field: "c"},
	)
	resOne, err := one.Sort(rows)
	if err != nil {
		t.Fatalf("Sort one key: %v", err)
	}
	resThree, err := three.Sort(rows)
	if err != nil {
		t.Fatalf("Sort three keys: %v", err)
	}
	if resOne.Stats.Comparisons == 0 {
		t.Fatal("Comparisons = 0, want > 0")
	}
	if resOne.Stats.Comparisons != resThree.Stats.Comparisons {
		t.Fatalf("comparisons grew with key count: 1 key = %d, 3 keys = %d",
			resOne.Stats.Comparisons, resThree.Stats.Comparisons)
	}
	if !reflect.DeepEqual(resOne.Indices(), resThree.Indices()) {
		t.Fatalf("order changed: %v vs %v", resOne.Indices(), resThree.Indices())
	}
}

// 空键列表：所有行相等，输出保持输入顺序。
func TestNoKeysIsStable(t *testing.T) {
	rows := allEqualRows(10)
	res, err := NewSorter().Sort(rows)
	if err != nil {
		t.Fatalf("Sort: %v", err)
	}
	for i, row := range res.Rows {
		if row.Index != i {
			t.Fatalf("position %d holds input index %d", i, row.Index)
		}
	}
}
