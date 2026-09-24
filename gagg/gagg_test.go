package gagg

import (
	"errors"
	"testing"

	"ontology/gdelta"
)

// TestIncrementalReads pins O(1) aggregate maintenance: the number of rows
// read while applying one op must not grow with the group size m. It reads the
// unexported `reads` field directly; no exported API exposes its value.
func TestIncrementalReads(t *testing.T) {
	const bound int64 = 2 // m-independent ceiling; a single op touches only its id
	for _, m := range []int64{100, 1000, 10000} {
		tb := New(2)
		for i := int64(1); i <= m; i++ {
			if _, err := tb.Apply([]gdelta.Op{gdelta.Insert(i, "g", 1)}); err != nil {
				t.Fatalf("m=%d seed: %v", m, err)
			}
		}
		if _, err := tb.Apply([]gdelta.Op{gdelta.Insert(m+1, "h", 1)}); err != nil {
			t.Fatalf("m=%d seed h: %v", m, err)
		}
		probes := []struct {
			name string
			op   gdelta.Op
		}{
			{"same-key update", gdelta.Update(1, "g", 9)},
			{"key-change update", gdelta.Update(2, "h", 9)},
			{"delete", gdelta.Delete(3)},
		}
		for _, p := range probes {
			if _, err := tb.Apply([]gdelta.Op{p.op}); err != nil {
				t.Fatalf("m=%d %s: %v", m, p.name, err)
			}
			if tb.reads > bound {
				t.Fatalf("m=%d %s read %d rows, want <= %d (linear rescan?)", m, p.name, tb.reads, bound)
			}
		}
	}
}

// TestMinimalOutput pins gdelta's per-group rendering: unchanged emits
// nothing, count->0 emits only '-', birth emits only '+', sum==0 keeps a
// count>0 group, and one group yields at most one '-' then one '+'.
func TestMinimalOutput(t *testing.T) {
	cases := []struct {
		name        string
		before, aft gdelta.Agg
		want        int
		firstAdd    bool
	}{
		{"unchanged", gdelta.Agg{Sum: 3, Count: 2}, gdelta.Agg{Sum: 3, Count: 2}, 0, false},
		{"birth", gdelta.Agg{Count: 0}, gdelta.Agg{Sum: 5, Count: 1}, 1, true},
		{"death", gdelta.Agg{Sum: 5, Count: 1}, gdelta.Agg{Count: 0}, 1, false},
		{"sum-zero-kept", gdelta.Agg{Sum: 5, Count: 1}, gdelta.Agg{Sum: 0, Count: 2}, 2, false},
		{"value-change", gdelta.Agg{Sum: 0, Count: 2}, gdelta.Agg{Sum: 3, Count: 2}, 2, false},
	}
	for _, c := range cases {
		got := gdelta.Entries("g", c.before, c.aft)
		if len(got) != c.want {
			t.Fatalf("%s: got %d entries, want %d", c.name, len(got), c.want)
		}
		if c.want > 0 && got[0].Add != c.firstAdd {
			t.Fatalf("%s: first entry Add=%v want %v", c.name, got[0].Add, c.firstAdd)
		}
		if c.want == 2 && (got[0].Add || !got[1].Add) {
			t.Fatalf("%s: expected '-' then '+'", c.name)
		}
	}
	// Same-key Update (even a moved-then-net case) never splits into two pairs.
	old := gdelta.Row{G: "g", V: 1}
	d := gdelta.Deltas(&old, &gdelta.Row{G: "g", V: 4})
	if len(d) != 1 || d[0].DCount != 0 || d[0].DSum != 3 {
		t.Fatalf("same-key update must be one net delta, got %+v", d)
	}
}

// TestRejectedBatchAtomic pins failure-no-trace for all four distinct
// sentinels, including a legal op placed before the rejected one, and that the
// table stays usable afterwards.
func TestRejectedBatchAtomic(t *testing.T) {
	cases := []struct {
		name string
		seed []gdelta.Op
		bad  []gdelta.Op
		want error
	}{
		{"row exists", []gdelta.Op{gdelta.Insert(1, "a", 1)}, []gdelta.Op{gdelta.Insert(1, "b", 2)}, ErrRowExists},
		{"row missing update", nil, []gdelta.Op{gdelta.Update(7, "a", 1)}, ErrRowNotFound},
		{"row missing delete", nil, []gdelta.Op{gdelta.Delete(7)}, ErrRowNotFound},
		{"empty group", nil, []gdelta.Op{gdelta.Insert(1, "", 1)}, ErrEmptyGroup},
		{"too many groups", []gdelta.Op{gdelta.Insert(1, "a", 1)}, []gdelta.Op{gdelta.Insert(2, "b", 1), gdelta.Insert(3, "c", 1)}, ErrTooMany},
		{"valid then invalid", nil, []gdelta.Op{gdelta.Insert(1, "a", 1), gdelta.Insert(2, "", 1)}, ErrEmptyGroup},
	}
	for _, c := range cases {
		tb := New(2)
		for _, s := range c.seed {
			if _, err := tb.Apply([]gdelta.Op{s}); err != nil {
				t.Fatalf("%s seed: %v", c.name, err)
			}
		}
		beforeRows, beforeAgg := len(tb.rows), len(tb.agg)
		out, err := tb.Apply(c.bad)
		if !errors.Is(err, c.want) {
			t.Fatalf("%s: err=%v want %v", c.name, err, c.want)
		}
		if out != nil || len(tb.rows) != beforeRows || len(tb.agg) != beforeAgg {
			t.Fatalf("%s: rejected batch left a trace out=%v rows=%d agg=%d", c.name, out, len(tb.rows), len(tb.agg))
		}
		if _, err := tb.Apply([]gdelta.Op{gdelta.Insert(99, "z", 1)}); err != nil && c.name != "too many groups" {
			t.Fatalf("%s: table unusable after reject: %v", c.name, err)
		}
	}
	// The four failure classes must be mutually distinguishable.
	if errors.Is(ErrRowExists, ErrRowNotFound) || ErrRowExists == ErrEmptyGroup || ErrEmptyGroup == ErrTooMany {
		t.Fatal("sentinel errors must be distinct")
	}
}
