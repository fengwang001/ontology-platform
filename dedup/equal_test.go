package dedup

import (
	"math"
	"testing"
)

func groupCount(rows ...map[string]any) int {
	d := New([]string{"v"}, KeepFirst)
	for _, r := range rows {
		d.Add(r)
	}
	return d.Groups()
}

func TestInt64AndFloat64SameGroup(t *testing.T) {
	if got := groupCount(
		map[string]any{"v": int64(3)},
		map[string]any{"v": float64(3.0)},
	); got != 1 {
		t.Fatalf("int64(3) vs float64(3.0): got %d groups, want 1", got)
	}
	if got := groupCount(
		map[string]any{"v": int64(3)},
		map[string]any{"v": float64(3.5)},
	); got != 2 {
		t.Fatalf("int64(3) vs float64(3.5): got %d groups, want 2", got)
	}
	if got := groupCount(
		map[string]any{"v": int64(-7)},
		map[string]any{"v": float64(-7.0)},
	); got != 1 {
		t.Fatalf("int64(-7) vs float64(-7.0): got %d groups, want 1", got)
	}
}

func TestSignedZeroSameGroup(t *testing.T) {
	negZero := math.Copysign(0, -1)
	if got := groupCount(
		map[string]any{"v": 0.0},
		map[string]any{"v": negZero},
		map[string]any{"v": int64(0)},
	); got != 1 {
		t.Fatalf("+0.0/-0.0/0: got %d groups, want 1", got)
	}
}

func TestNaNEachRowOwnGroup(t *testing.T) {
	d := New([]string{"v"}, KeepFirst)
	nan := math.NaN()
	d.Add(map[string]any{"v": nan, "tag": "a"})
	d.Add(map[string]any{"v": nan, "tag": "b"})
	d.Add(map[string]any{"v": nan, "tag": "c"})
	d.Add(map[string]any{"v": 1.0})
	d.Add(map[string]any{"v": 1.0})
	if got := d.Groups(); got != 4 {
		t.Fatalf("got %d groups, want 4 (3 NaN + 1 numeric)", got)
	}
	if got := d.NaNGroups(); got != 3 {
		t.Fatalf("NaNGroups = %d, want 3", got)
	}
	if got := d.Processed(); got != 5 {
		t.Fatalf("Processed = %d, want 5", got)
	}
}

func TestStringNotEqualNumber(t *testing.T) {
	if got := groupCount(
		map[string]any{"v": "3"},
		map[string]any{"v": int64(3)},
		map[string]any{"v": float64(3.0)},
	); got != 2 {
		t.Fatalf("string vs number: got %d groups, want 2", got)
	}
}

func TestBoolOnlyEqualBool(t *testing.T) {
	if got := groupCount(
		map[string]any{"v": true},
		map[string]any{"v": int64(1)},
		map[string]any{"v": "true"},
	); got != 3 {
		t.Fatalf("bool vs int vs string: got %d groups, want 3", got)
	}
	if got := groupCount(
		map[string]any{"v": true},
		map[string]any{"v": true},
	); got != 1 {
		t.Fatalf("bool vs bool: got %d groups, want 1", got)
	}
}

func TestUncomparableTypeNoError(t *testing.T) {
	d := New([]string{"v"}, KeepFirst)
	d.Add(map[string]any{"v": []int{1, 2}})
	d.Add(map[string]any{"v": []int{1, 2}})
	d.Add(map[string]any{"v": map[string]int{"a": 1}})
	if got := d.Groups(); got != 3 {
		t.Fatalf("uncomparable values: got %d groups, want 3", got)
	}
	if got := d.NaNGroups(); got != 0 {
		t.Fatalf("uncomparable must not count as NaN: got %d", got)
	}
}

func TestMixedTypeSortOrderDeterministic(t *testing.T) {
	rows := []map[string]any{
		{"v": "apple"}, {"v": int64(2)}, {"v": true}, {"v": float64(1.5)},
		{"v": nil}, {"v": ""}, {"v": false}, {"v": int64(-1)},
	}
	a := New([]string{"v"}, KeepFirst)
	b := New([]string{"v"}, KeepFirst)
	for _, r := range rows {
		a.Add(r)
	}
	for i := len(rows) - 1; i >= 0; i-- {
		b.Add(rows[i])
	}
	sa, sb := a.Snapshot(), b.Snapshot()
	if len(sa) != len(rows) {
		t.Fatalf("got %d rows, want %d", len(sa), len(rows))
	}
	for i := range sa {
		if sa[i]["v"] != sb[i]["v"] {
			t.Fatalf("row %d differs between arrival orders: %v vs %v",
				i, sa[i]["v"], sb[i]["v"])
		}
	}
}
