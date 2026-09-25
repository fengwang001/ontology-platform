package store

import (
	"errors"
	"strconv"
	"testing"
)

type tOp struct {
	del  bool
	k, v string
}

var seven = []tOp{
	{false, "a", "1"}, {false, "b", "2"}, {false, "c", "3"}, {true, "c", ""},
	{false, "b", "9"}, {false, "c", "7"}, {false, "c", "8"},
}

func apply(t *testing.T, st *Store, op tOp) {
	t.Helper()
	var err error
	if op.del {
		err = st.Del(op.k)
	} else {
		err = st.Set(op.k, op.v)
	}
	if err != nil {
		t.Fatal(err)
	}
}

// TestSevenStepDerivation checks delta/base/compaction after each of the
// seven prescribed steps (NOTES.md table).
func TestSevenStepDerivation(t *testing.T) {
	wantDelta := []int{1, 2, 3, 0, 1, 2, 3}
	st, _ := New(4)
	for i, op := range seven {
		apply(t, st, op)
		if st.DeltaLen() != wantDelta[i] {
			t.Fatalf("step %d: delta len %d, want %d", i+1, st.DeltaLen(), wantDelta[i])
		}
		if i == 3 {
			if bk := st.BaseKeys(); len(bk) != 2 || bk[0] != "a" || bk[1] != "b" {
				t.Fatalf("step 4: base keys %v, want [a b]", bk)
			}
		}
	}
}

// TestNaiveRecompute: Read must equal folding all history onto an empty base.
func TestNaiveRecompute(t *testing.T) {
	cases := [][]tOp{
		seven,
		{{false, "a", "1"}, {false, "a", "2"}, {true, "a", ""}, {false, "a", "3"}},
		{{false, "x", "0"}, {true, "x", ""}, {false, "y", "1"}, {true, "x", ""}},
	}
	for ci, ops := range cases {
		st, _ := New(4)
		ref := map[string]bool{}
		vals := map[string]string{}
		for _, op := range ops {
			apply(t, st, op)
			if op.del {
				ref[op.k] = false
			} else {
				ref[op.k], vals[op.k] = true, op.v
			}
		}
		ref["absent"] = false
		for k, wantOk := range ref {
			gv, gok := st.Read(k)
			if gok != wantOk || (wantOk && gv != vals[k]) {
				t.Fatalf("case %d %q: got (%q,%v), want (%q,%v)", ci, k, gv, gok, vals[k], wantOk)
			}
		}
	}
}

// TestTombstoneSemantics: delete leaves no empty-valued key; rewrite wins.
func TestTombstoneSemantics(t *testing.T) {
	cases := []struct {
		ops        []tOp
		wantVal    string
		wantOk     bool
		wantInBase bool
	}{
		{[]tOp{{false, "k", "v"}, {true, "k", ""}}, "", false, false},
		{[]tOp{{false, "k", "v"}, {true, "k", ""}, {false, "k", "w"}}, "w", true, true},
	}
	for _, c := range cases {
		st, _ := New(4)
		for _, op := range c.ops {
			apply(t, st, op)
		}
		for i := 0; i < 4; i++ { // pad to force compaction so base is observable
			apply(t, st, tOp{k: "pad" + strconv.Itoa(i), v: "p"})
		}
		if v, ok := st.Read("k"); v != c.wantVal || ok != c.wantOk {
			t.Fatalf("Read(k)=(%q,%v), want (%q,%v)", v, ok, c.wantVal, c.wantOk)
		}
		if _, inBase := st.base["k"]; inBase != c.wantInBase {
			t.Fatalf("base[k] presence %v, want %v, base=%v", inBase, c.wantInBase, st.base)
		}
	}
}

// TestMergeCostBounded: cost must not grow with total writes m, and
// len(delta) <= T-1 holds after every write along the way.
func TestMergeCostBounded(t *testing.T) {
	const T = 8
	for _, m := range []int{100, 1000, 10000} {
		st, _ := New(T)
		for i := 0; i < m; i++ {
			apply(t, st, tOp{k: "k" + strconv.Itoa(i), v: "v"})
			if st.DeltaLen() > T-1 {
				t.Fatalf("m=%d write %d: delta len %d > T-1", m, i+1, st.DeltaLen())
			}
		}
		_, _ = st.Read("k0")
		if cost := int(st.lastReadCost.Load()); cost > T-1 {
			t.Fatalf("m=%d: merge cost %d > T-1=%d", m, cost, T-1)
		}
	}
}

// TestRejectLeavesNoTrace: distinct sentinels; rejection mutates nothing.
func TestRejectLeavesNoTrace(t *testing.T) {
	for _, bad := range []int{0, -1, -100} {
		if _, err := New(bad); !errors.Is(err, ErrBadThreshold) {
			t.Fatalf("New(%d) err = %v", bad, err)
		}
	}
	st, _ := New(4)
	apply(t, st, tOp{k: "a", v: "1"})
	apply(t, st, tOp{k: "b", v: "2"})
	_, _ = st.Read("a")
	costBefore, deltaBefore := st.lastReadCost.Load(), st.DeltaLen()
	keysBefore := len(st.BaseKeys())
	e1, e2 := st.Set("", "x"), st.Del("")
	if !errors.Is(e1, ErrEmptyKey) || !errors.Is(e2, ErrEmptyKey) || errors.Is(e1, ErrBadThreshold) {
		t.Fatalf("want two distinct sentinels, got %v and %v", e1, e2)
	}
	if st.DeltaLen() != deltaBefore || st.lastReadCost.Load() != costBefore || len(st.BaseKeys()) != keysBefore {
		t.Fatal("state mutated after rejected calls")
	}
	apply(t, st, tOp{k: "a", v: "9"})
	if v, ok := st.Read("a"); !ok || v != "9" {
		t.Fatalf("store unusable after rejection: (%q,%v)", v, ok)
	}
}
