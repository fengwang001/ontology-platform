package ontology

import (
	"math"
	"reflect"
	"testing"
)

// comboRows mixes one null row with two valued rows.
func comboRows() []map[string]any {
	return []map[string]any{
		{"v": int64(2)},
		{"v": nil},
		{"v": int64(1)},
	}
}

func TestNullPlacementFourCombinations(t *testing.T) {
	cases := []struct {
		name string
		key  SortKey
		want []int
	}{
		{"AscNullsFirst", SortKey{Field: "v", NullsFirst: true}, []int{1, 2, 0}},
		{"AscNullsLast", SortKey{Field: "v"}, []int{2, 0, 1}},
		{"DescNullsFirst", SortKey{Field: "v", Desc: true, NullsFirst: true}, []int{1, 0, 2}},
		{"DescNullsLast", SortKey{Field: "v", Desc: true}, []int{0, 2, 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := New(tc.key).Sort(comboRows())
			if err != nil {
				t.Fatalf("Sort: %v", err)
			}
			if !reflect.DeepEqual(res.Indices, tc.want) {
				t.Fatalf("Indices = %v, want %v", res.Indices, tc.want)
			}
		})
	}
}

func TestMissingAndNilSamePositionButDistinguishable(t *testing.T) {
	rows := []map[string]any{
		{"v": int64(1)},
		{"v": nil},
		{"w": 0}, // "v" missing entirely
	}
	res, err := New(SortKey{Field: "v", NullsFirst: true}).Sort(rows)
	if err != nil {
		t.Fatalf("Sort: %v", err)
	}
	// Both nulls sort before the value; their relative order is stable.
	if !reflect.DeepEqual(res.Indices, []int{1, 2, 0}) {
		t.Fatalf("Indices = %v, want [1 2 0]", res.Indices)
	}
	if got := NullKindOf(rows[1], "v"); got != NilValue {
		t.Fatalf("NullKindOf(nil) = %v, want NilValue", got)
	}
	if got := NullKindOf(rows[2], "v"); got != Missing {
		t.Fatalf("NullKindOf(missing) = %v, want Missing", got)
	}
	if got := NullKindOf(rows[0], "v"); got != NotNull {
		t.Fatalf("NullKindOf(value) = %v, want NotNull", got)
	}
}

func TestEmptyStringIsNormalValue(t *testing.T) {
	rows := []map[string]any{
		{"s": "b"},
		{"s": nil},
		{"s": ""},
	}
	res, err := New(SortKey{Field: "s", NullsFirst: true}).Sort(rows)
	if err != nil {
		t.Fatalf("Sort: %v", err)
	}
	// nil first (null), then "" before "b" as ordinary strings.
	if !reflect.DeepEqual(res.Indices, []int{1, 2, 0}) {
		t.Fatalf("Indices = %v, want [1 2 0]", res.Indices)
	}
	if got := NullKindOf(rows[2], "s"); got != NotNull {
		t.Fatalf("empty string NullKind = %v, want NotNull", got)
	}
}

func TestNaNTreatedAsNullAndCounted(t *testing.T) {
	rows := []map[string]any{
		{"v": math.NaN()},
		{"v": 2.5},
		{"v": math.NaN()},
		{"v": 1.5},
	}
	res, err := New(SortKey{Field: "v", NullsFirst: true}).Sort(rows)
	if err != nil {
		t.Fatalf("Sort: %v", err)
	}
	// NaNs first (stable among themselves), then ascending numbers.
	if !reflect.DeepEqual(res.Indices, []int{0, 2, 3, 1}) {
		t.Fatalf("Indices = %v, want [0 2 3 1]", res.Indices)
	}
	if res.NaNs != 2 {
		t.Fatalf("NaNs = %d, want 2", res.NaNs)
	}
	if got := NullKindOf(rows[0], "v"); got != NaNValue {
		t.Fatalf("NullKindOf(NaN) = %v, want NaNValue", got)
	}
}

func TestSignedZerosEqualOrderedByStability(t *testing.T) {
	rows := []map[string]any{
		{"v": math.Copysign(0, -1)}, // -0.0
		{"v": 0.0},                  // +0.0
		{"v": int64(0)},
		{"v": math.Copysign(0, -1)},
	}
	res, err := New(SortKey{Field: "v"}).Sort(rows)
	if err != nil {
		t.Fatalf("Sort: %v", err)
	}
	if !reflect.DeepEqual(res.Indices, []int{0, 1, 2, 3}) {
		t.Fatalf("Indices = %v, want stable identity", res.Indices)
	}
}
