package diff

import (
	"reflect"
	"strconv"
	"testing"
)

func TestOnlyChangedColumns(t *testing.T) {
	cases := []struct {
		name   string
		before map[string]string
		after  map[string]string
		want   []Change
	}{
		{"empty to two", nil, map[string]string{"a": "1", "b": "x"},
			[]Change{{Kind: Added, Col: "a", New: "1"}, {Kind: Added, Col: "b", New: "x"}}},
		{"added empty-string value exists", map[string]string{"a": "1"},
			map[string]string{"a": "1", "c": ""}, []Change{{Kind: Added, Col: "c", New: ""}}},
		{"removed carries old value", map[string]string{"a": "2", "b": "y"},
			map[string]string{"a": "2"}, []Change{{Kind: Removed, Col: "b", Old: "y"}}},
		{"changed real values", map[string]string{"a": "1"},
			map[string]string{"a": "2"}, []Change{{Kind: Changed, Col: "a", Old: "1", New: "2"}}},
		{"equal non-empty skipped", map[string]string{"a": "1"}, map[string]string{"a": "1"}, nil},
		{"empty equals empty skipped, not removed/added",
			map[string]string{"c": ""}, map[string]string{"c": ""}, nil},
		{"mixed step 5", map[string]string{"a": "2", "c": ""}, map[string]string{"c": "z"},
			[]Change{{Kind: Removed, Col: "a", Old: "2"}, {Kind: Changed, Col: "c", Old: "", New: "z"}}},
		{"clear whole row", map[string]string{"c": "z"}, map[string]string{},
			[]Change{{Kind: Removed, Col: "c", Old: "z"}}},
	}
	d := New()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := d.Diff(tc.before, tc.after)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOrderingAndEmptyString(t *testing.T) {
	cases := []struct {
		name   string
		before map[string]string
		after  map[string]string
		cols   []string // expected ordered column names
	}{
		{"all three kinds sorted", map[string]string{"z": "1", "m": "1"},
			map[string]string{"m": "2", "a": "", "z": "1"}, []string{"a", "m"}},
		{"removals sorted", map[string]string{"b": "", "a": "", "c": ""},
			map[string]string{}, []string{"a", "b", "c"}},
		{"empty-string value is not absence", map[string]string{"c": ""},
			map[string]string{"c": "z"}, []string{"c"}},
	}
	d := New()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := d.Diff(tc.before, tc.after)
			if len(got) != len(tc.cols) {
				t.Fatalf("got %d changes %v, want %d", len(got), got, len(tc.cols))
			}
			for i, ch := range got {
				if ch.Col != tc.cols[i] {
					t.Fatalf("position %d: got col %q, want %q (order: %v)", i, ch.Col, tc.cols[i], got)
				}
			}
		})
	}
}

func TestComparisonCounter(t *testing.T) {
	d := New()
	for _, m := range []int{100, 1000, 10000} {
		before := make(map[string]string, m)
		diff := make(map[string]string, m)
		for i := 0; i < m; i++ {
			c := strconv.Itoa(i)
			before[c] = "b" + c
			diff[c] = "a" + c
		}
		d.Diff(before, diff)
		if d.compares != m { // one comparison per shared column: single linear merge, not m^2
			t.Fatalf("m=%d: got %d comparisons, want exactly %d", m, d.compares, m)
		}
		d.Diff(diff, diff)
		if d.compares != 0 { // identical rows short-circuit on the fingerprint
			t.Fatalf("m=%d identical rows: got %d comparisons, want 0", m, d.compares)
		}
		// Added/Removed columns are membership tests only and never compared.
		lBefore := map[string]string{"0": "b0", "1": "b1", "2": "b2", "3": "b3", "4": "b4", "5": "b5", "6": "b6", "7": "b7"}
		lAfter := map[string]string{"0": "x", "1": "x", "2": "x", "3": "x", "4": "x", "8": "x", "9": "x"}
		d.Diff(lBefore, lAfter)
		if d.compares != 5 { // shared columns 0..4 compared once; 5..9 never value-compared
			t.Fatalf("lopsided: got %d comparisons, want 5", d.compares)
		}
	}
}
