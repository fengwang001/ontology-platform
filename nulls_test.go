package ontology

import (
	"math"
	"reflect"
	"testing"
)

// 一行空值（nil）加两行正常值，验证四种 升降序×空值位置 组合。
func TestNullsOrthogonalToDirection(t *testing.T) {
	rows := []map[string]any{
		{"k": int64(2)},
		{"k": nil},
		{"k": int64(1)},
	}
	cases := []struct {
		name string
		key  SortKey
		want []int
	}{
		{"AscNullsFirst", SortKey{Field: "k", NullsFirst: true}, []int{1, 2, 0}},
		{"AscNullsLast", SortKey{Field: "k"}, []int{2, 0, 1}},
		{"DescNullsFirst", SortKey{Field: "k", Desc: true, NullsFirst: true}, []int{1, 0, 2}},
		{"DescNullsLast", SortKey{Field: "k", Desc: true}, []int{0, 2, 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := NewSorter(tc.key).Sort(rows)
			if err != nil {
				t.Fatalf("Sort: %v", err)
			}
			if got := res.Indices(); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("indices = %v, want %v", got, tc.want)
			}
		})
	}
}

// 缺失与 nil 排序位置相同，但 Classify 能区分二者。
func TestMissingAndNilSamePositionButDistinguishable(t *testing.T) {
	rows := []map[string]any{
		{"k": nil},
		{"k": int64(5)},
		{"other": int64(9)},
	}
	for _, nullsFirst := range []bool{true, false} {
		res, err := NewSorter(SortKey{Field: "k", NullsFirst: nullsFirst}).Sort(rows)
		if err != nil {
			t.Fatalf("Sort: %v", err)
		}
		var want []int
		if nullsFirst {
			want = []int{0, 2, 1} // 两个空值按稳定性保持 0 在 2 前
		} else {
			want = []int{1, 0, 2}
		}
		if got := res.Indices(); !reflect.DeepEqual(got, want) {
			t.Fatalf("nullsFirst=%v indices = %v, want %v", nullsFirst, got, want)
		}
	}
	if got := Classify(rows[0], "k"); got != NullNil {
		t.Fatalf("Classify nil = %v, want NullNil", got)
	}
	if got := Classify(rows[2], "k"); got != NullMissing {
		t.Fatalf("Classify missing = %v, want NullMissing", got)
	}
	if got := Classify(rows[1], "k"); got != NotNull {
		t.Fatalf("Classify value = %v, want NotNull", got)
	}
}

// 空字符串是正常值：不参与空值规则，按字符串比较。
func TestEmptyStringIsNormalValue(t *testing.T) {
	rows := []map[string]any{
		{"k": "b"},
		{"k": ""},
		{"k": nil},
	}
	res, err := NewSorter(SortKey{Field: "k"}).Sort(rows) // 升序 + 空值在后
	if err != nil {
		t.Fatalf("Sort: %v", err)
	}
	if got := res.Indices(); !reflect.DeepEqual(got, []int{1, 0, 2}) {
		t.Fatalf("indices = %v, want [1 0 2]", got)
	}
	if got := Classify(rows[1], "k"); got != NotNull {
		t.Fatalf("Classify empty string = %v, want NotNull", got)
	}
}

// NaN 与空值同等对待：走 NullsFirst/NullsLast，且计入 NaNValues。
func TestNaNTreatedAsNullAndCounted(t *testing.T) {
	rows := []map[string]any{
		{"k": math.NaN()},
		{"k": 1.5},
		{"k": nil},
	}
	res, err := NewSorter(SortKey{Field: "k", NullsFirst: true}).Sort(rows)
	if err != nil {
		t.Fatalf("Sort: %v", err)
	}
	if got := res.Indices(); !reflect.DeepEqual(got, []int{0, 2, 1}) {
		t.Fatalf("indices = %v, want [0 2 1]", got)
	}
	if res.Stats.NaNValues == 0 {
		t.Fatal("NaNValues = 0, want > 0")
	}
	resLast, err := NewSorter(SortKey{Field: "k"}).Sort(rows)
	if err != nil {
		t.Fatalf("Sort: %v", err)
	}
	if got := resLast.Indices(); !reflect.DeepEqual(got, []int{1, 0, 2}) {
		t.Fatalf("nullsLast indices = %v, want [1 0 2]", got)
	}
	if got := Classify(rows[0], "k"); got != NullNaN {
		t.Fatalf("Classify NaN = %v, want NullNaN", got)
	}
}

// +0.0 与 -0.0 视为相等，先后由稳定性决定。
func TestSignedZerosEqual(t *testing.T) {
	rows := []map[string]any{
		{"k": 0.0, "tag": 0},
		{"k": math.Copysign(0, -1), "tag": 1},
		{"k": 1.0, "tag": 2},
	}
	res, err := NewSorter(SortKey{Field: "k"}).Sort(rows)
	if err != nil {
		t.Fatalf("Sort: %v", err)
	}
	if got := res.Indices(); !reflect.DeepEqual(got, []int{0, 1, 2}) {
		t.Fatalf("indices = %v, want [0 1 2]", got)
	}
}
