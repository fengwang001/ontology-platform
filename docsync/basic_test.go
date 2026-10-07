package docsync

import (
	"bytes"
	"testing"
)

func rng(aL, aC, bL, bC int) Range {
	return Range{Start: Position{aL, aC}, End: Position{bL, bC}}
}

func TestEmptyDocumentHasOneLine(t *testing.T) {
	st := NewStore("")
	if got := st.Text(); got != "" {
		t.Fatalf("text = %q", got)
	}
	if off, err := st.OffsetOf(Position{0, 0}); err != nil || off != 0 {
		t.Fatalf("(0,0) = %d,%v", off, err)
	}
	if _, err := st.OffsetOf(Position{1, 0}); err == nil {
		t.Fatal("line 1 must not exist in empty document")
	}
	if p, err := st.PositionOf(0); err != nil || p != (Position{0, 0}) {
		t.Fatalf("offset 0 = %v,%v", p, err)
	}
}

func TestAstralColumns(t *testing.T) {
	// "a😀b": a=0, emoji occupies UTF-16 units 1 and 2, b=3.
	st := NewStore("a😀b")
	cases := []struct {
		col   int
		kind  RejectKind
		valid bool
	}{
		{0, 0, true},
		{1, 0, true},
		{2, RejectSurrogateSplit, false},
		{3, 0, true},
		{4, 0, true},
		{5, RejectOutOfBounds, false},
	}
	for _, c := range cases {
		off, err := st.OffsetOf(Position{0, c.col})
		if c.valid {
			if err != nil || off != c.col {
				t.Fatalf("col %d -> off %d err %v", c.col, off, err)
			}
			p, perr := st.PositionOf(c.col)
			if perr != nil || p.Character != c.col {
				t.Fatalf("off %d -> %v %v", c.col, p, perr)
			}
			continue
		}
		re, ok := err.(*RejectError)
		if !ok || re.Kind != c.kind {
			t.Fatalf("col %d err = %v want %d", c.col, err, c.kind)
		}
	}
}

func TestCarriageReturnIsOrdinary(t *testing.T) {
	st := NewStore("a\rb\rc")
	if st.Text() != "a\rb\rc" {
		t.Fatalf("CR altered: %q", st.Text())
	}
	if off, _ := st.OffsetOf(Position{0, 3}); off != 3 {
		t.Fatalf("line length = %d", off)
	}
	if _, err := st.OffsetOf(Position{1, 0}); err == nil {
		t.Fatal("CR must not split lines")
	}
}

func TestLinesAreLFOnly(t *testing.T) {
	st := NewStore("ab\ncde\nf")
	want := []struct {
		p Position
		o int
	}{
		{Position{0, 0}, 0},
		{Position{0, 2}, 2},
		{Position{1, 0}, 3},
		{Position{1, 3}, 6},
		{Position{2, 0}, 7},
		{Position{2, 1}, 8},
	}
	for _, w := range want {
		if off, err := st.OffsetOf(w.p); err != nil || off != w.o {
			t.Fatalf("%v -> %d,%v want %d", w.p, off, err, w.o)
		}
		if p, err := st.PositionOf(w.o); err != nil || p != w.p {
			t.Fatalf("%d -> %v,%v want %v", w.o, p, err, w.p)
		}
	}
}

func TestAstralPositionRejectsDistinguish(t *testing.T) {
	st := NewStore("😀x\n😀")
	// Column 1 on either line splits a surrogate pair.
	for _, p := range []Position{{0, 1}, {1, 1}} {
		_, err := st.OffsetOf(p)
		re, ok := err.(*RejectError)
		if !ok || re.Kind != RejectSurrogateSplit {
			t.Fatalf("%v -> %v", p, err)
		}
	}
	_, err := st.OffsetOf(Position{1, 3})
	if re, ok := err.(*RejectError); !ok || re.Kind != RejectOutOfBounds {
		t.Fatalf("col past end -> %v", err)
	}
}

func TestStaleHasPriorityOverBounds(t *testing.T) {
	st := NewStore("abc")
	if _, err := st.Apply(0, []Edit{{Range: rng(0, 0, 0, 3), Text: "x"}}); err != nil {
		t.Fatal(err)
	}
	_, err := st.Apply(0, []Edit{{Range: rng(0, 9, 0, 9), Text: "y"}})
	re, ok := err.(*RejectError)
	if !ok || re.Kind != RejectStale {
		t.Fatalf("want stale, got %v", err)
	}
}

func TestBoundsBeforeSurrogate(t *testing.T) {
	st := NewStore("😀")
	_, err := st.OffsetOf(Position{0, 9})
	if re, _ := err.(*RejectError); re.Kind != RejectOutOfBounds {
		t.Fatalf("want OOB, got %v", err)
	}
}

func TestReversedRangePriority(t *testing.T) {
	st := NewStore("abcde")
	_, err := st.Apply(0, []Edit{{Range: rng(0, 4, 0, 1), Text: ""}})
	if re, _ := err.(*RejectError); re.Kind != RejectReversedRange {
		t.Fatalf("want reversed, got %v", err)
	}
}

func TestSurrogateBeforeOverlap(t *testing.T) {
	st := NewStore("😀😀")
	_, err := st.Apply(0, []Edit{
		{Range: rng(0, 1, 0, 1), Text: "x"},
		{Range: rng(0, 0, 0, 0), Text: "y"},
	})
	if re, _ := err.(*RejectError); re.Kind != RejectSurrogateSplit {
		t.Fatalf("want surrogate split, got %v", err)
	}
}

func TestSimultaneousEdits(t *testing.T) {
	st := NewStore("abcdef")
	// Touching edits: replace [0,2) and [2,4), plus same-point inserts.
	v, err := st.Apply(0, []Edit{
		{Range: rng(0, 0, 0, 2), Text: "XY"},
		{Range: rng(0, 2, 0, 4), Text: "ZW"},
		{Range: rng(0, 6, 0, 6), Text: "P"},
		{Range: rng(0, 6, 0, 6), Text: "Q"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if v != 1 || st.Text() != "XYZWefPQ" {
		t.Fatalf("v=%d text=%q", v, st.Text())
	}
}

func TestOverlapRejectedAndStateUntouched(t *testing.T) {
	st := NewStore("abcdef")
	before := st.Text()
	_, err := st.Apply(0, []Edit{
		{Range: rng(0, 0, 0, 3), Text: "X"},
		{Range: rng(0, 2, 0, 5), Text: "Y"},
	})
	if re, _ := err.(*RejectError); re.Kind != RejectOverlappingEdits {
		t.Fatalf("want overlap, got %v", err)
	}
	if st.Text() != before || st.Version() != 0 {
		t.Fatal("rejected change mutated state")
	}
	// Touching non-empty edits are legal.
	if _, err := st.Apply(0, []Edit{
		{Range: rng(0, 0, 0, 3), Text: "X"},
		{Range: rng(0, 3, 0, 5), Text: "Y"},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRejectedChangePreservesDiagnostics(t *testing.T) {
	st := NewStore("abcdef")
	if _, err := st.Register(0, Diagnostic{Range: rng(0, 0, 0, 1), Severity: 1, Message: "d"}); err != nil {
		t.Fatal(err)
	}
	_, _ = st.Apply(0, []Edit{{Range: rng(0, 2, 0, 4), Text: "Z"}}) // version 1
	_, err := st.Apply(0, []Edit{{Range: rng(0, 0, 0, 9), Text: "!"}})
	if re, _ := err.(*RejectError); re.Kind != RejectStale {
		t.Fatalf("got %v", err)
	}
	snap := st.Snapshot()
	if len(snap.Diagnostics) != 1 || len(snap.Invalidated) != 0 {
		t.Fatalf("diagnostics mutated by stale change: %+v", snap)
	}
}

func TestLoggerEmits(t *testing.T) {
	var buf bytes.Buffer
	st := NewStore("abc", WithLogger(&buf))
	_, _ = st.Apply(0, []Edit{{Range: rng(0, 0, 0, 1), Text: "X"}})
	_, _ = st.Register(1, Diagnostic{Range: rng(0, 0, 0, 0), Message: "m"})
	_ = st.Snapshot()
	if buf.Len() == 0 {
		t.Fatal("logger wrote nothing")
	}
}
