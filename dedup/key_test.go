package dedup

import (
	"math"
	"testing"
)

func addRows(d *Deduper, rows ...map[string]any) {
	for _, r := range rows {
		d.Add(r)
	}
}

// int64 and float64 compare by numeric value: 3 and 3.0 are one group.
func TestInt64Float64SameGroup(t *testing.T) {
	d := New([]string{"v"}, KeepFirst)
	addRows(d,
		map[string]any{"v": int64(3), "tag": "int"},
		map[string]any{"v": float64(3.0), "tag": "float"},
		map[string]any{"v": 3, "tag": "int-alias"},
	)
	if got := d.GroupCount(); got != 1 {
		t.Fatalf("GroupCount=%d, want 1", got)
	}
	if got := d.Snapshot()[0].Row["tag"]; got != "int" {
		t.Fatalf("kept tag=%v, want int (KeepFirst)", got)
	}
	// A genuinely different value must not merge.
	d.Add(map[string]any{"v": float64(3.5)})
	if got := d.GroupCount(); got != 2 {
		t.Fatalf("GroupCount=%d, want 2 after 3.5", got)
	}
}

// +0.0 and -0.0 are the same group, and int64(0) joins them.
func TestSignedZeroSameGroup(t *testing.T) {
	d := New([]string{"v"}, KeepLast)
	addRows(d,
		map[string]any{"v": float64(0.0), "tag": "plus"},
		map[string]any{"v": math.Copysign(0, -1), "tag": "minus"},
		map[string]any{"v": int64(0), "tag": "int-zero"},
	)
	if got := d.GroupCount(); got != 1 {
		t.Fatalf("GroupCount=%d, want 1", got)
	}
	if got := d.Snapshot()[0].Row["tag"]; got != "int-zero" {
		t.Fatalf("kept tag=%v, want int-zero (KeepLast)", got)
	}
}

// NaN equals nothing, not even itself: every NaN row is its own group
// and is counted in NaNGroupCount.
func TestNaNEachRowOwnGroup(t *testing.T) {
	d := New([]string{"v"}, KeepFirst)
	nan := math.NaN()
	addRows(d,
		map[string]any{"v": nan, "tag": "n1"},
		map[string]any{"v": math.NaN(), "tag": "n2"},
		map[string]any{"v": nan, "tag": "n1-again"},
		map[string]any{"v": float64(1.5), "tag": "real"},
	)
	if got := d.GroupCount(); got != 4 {
		t.Fatalf("GroupCount=%d, want 4 (3 NaN + 1 real)", got)
	}
	if got := d.NaNGroupCount(); got != 3 {
		t.Fatalf("NaNGroupCount=%d, want 3", got)
	}
	if got := d.Processed(); got != 4 {
		t.Fatalf("Processed=%d, want 4", got)
	}
}

// Strings only equal strings, bools only equal bools.
func TestCrossTypeNotEqual(t *testing.T) {
	d := New([]string{"v"}, KeepFirst)
	addRows(d,
		map[string]any{"v": "3"},
		map[string]any{"v": int64(3)},
		map[string]any{"v": float64(3.0)},
		map[string]any{"v": true},
		map[string]any{"v": "true"},
		map[string]any{"v": int64(1)},
	)
	// "3" | 3=3.0 | true | "true" | 1 -> 5 groups.
	if got := d.GroupCount(); got != 5 {
		t.Fatalf("GroupCount=%d, want 5", got)
	}
}

// Values of incomparable / unsupported types never count as equal and
// must not cause errors.
func TestIncomparableTypesNeverEqual(t *testing.T) {
	d := New([]string{"v"}, KeepFirst)
	addRows(d,
		map[string]any{"v": []int{1, 2}},
		map[string]any{"v": []int{1, 2}},
		map[string]any{"v": map[string]int{"a": 1}},
	)
	if got := d.GroupCount(); got != 3 {
		t.Fatalf("GroupCount=%d, want 3 (each incomparable value unique)", got)
	}
}

// Missing, nil and empty string are three different groups, and the
// caller can tell which is which.
func TestEmptyThreeWaySplit(t *testing.T) {
	d := New([]string{"c"}, KeepFirst)
	addRows(d,
		map[string]any{"other": 1}, // missing
		map[string]any{"c": nil},   // nil
		map[string]any{"c": ""},    // empty string
		map[string]any{"c": ""},    // empty string again
		map[string]any{"other": 2}, // missing again
		map[string]any{"c": "x"},   // normal value
	)
	if got := d.GroupCount(); got != 4 {
		t.Fatalf("GroupCount=%d, want 4", got)
	}
	counts := d.EmptyGroupCounts()
	if counts[EmptyMissing] != 1 || counts[EmptyNil] != 1 || counts[EmptyString] != 1 {
		t.Fatalf("empty group counts=%v, want 1 each", counts)
	}
	seen := map[EmptyClass]bool{}
	for _, g := range d.Snapshot() {
		seen[g.Class] = true
		switch g.Class {
		case EmptyMissing:
			if _, ok := g.Row["c"]; ok {
				t.Fatal("missing group row unexpectedly has column c")
			}
		case EmptyNil:
			if v, ok := g.Row["c"]; !ok || v != nil {
				t.Fatal("nil group row must hold c=nil")
			}
		case EmptyString:
			if g.Row["c"] != "" {
				t.Fatal("empty-string group row must hold c=\"\"")
			}
		}
	}
	for _, c := range []EmptyClass{EmptyMissing, EmptyNil, EmptyString} {
		if !seen[c] {
			t.Fatalf("class %v not identifiable in snapshot", c)
		}
	}
}

// In multi-column dedup, any missing or nil column sends the whole row
// to the matching empty class; such rows are never dropped.
func TestMultiColumnEmptyClassification(t *testing.T) {
	d := New([]string{"a", "b"}, KeepFirst)
	addRows(d,
		map[string]any{"a": int64(1)},                // b missing
		map[string]any{"a": int64(2)},                // b missing, other key
		map[string]any{"a": int64(1), "b": nil},      // nil
		map[string]any{"a": "", "b": int64(5)},       // empty string
		map[string]any{"a": int64(1), "b": int64(5)}, // normal
	)
	if got := d.GroupCount(); got != 5 {
		t.Fatalf("GroupCount=%d, want 5 (nothing dropped)", got)
	}
	counts := d.EmptyGroupCounts()
	if counts[EmptyMissing] != 2 {
		t.Fatalf("missing groups=%d, want 2", counts[EmptyMissing])
	}
	if counts[EmptyNil] != 1 {
		t.Fatalf("nil groups=%d, want 1", counts[EmptyNil])
	}
	if counts[EmptyString] != 1 {
		t.Fatalf("empty-string groups=%d, want 1", counts[EmptyString])
	}
	if counts[EmptyNone] != 1 {
		t.Fatalf("normal groups=%d, want 1", counts[EmptyNone])
	}
}
