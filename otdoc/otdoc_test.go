package otdoc

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func r(n int) Comp    { return Comp{Kind: Retain, N: n} }
func i(s string) Comp { return Comp{Kind: Insert, Text: s} }
func d(n int) Comp    { return Comp{Kind: Delete, N: n} }

func opString(op Op) string {
	var b strings.Builder
	for _, c := range op {
		switch c.Kind {
		case Retain:
			fmt.Fprintf(&b, "R%d", c.N)
		case Insert:
			fmt.Fprintf(&b, "I%q", c.Text)
		case Delete:
			fmt.Fprintf(&b, "D%d", c.N)
		}
	}
	return b.String()
}

func opsEqual(a, b Op) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if a[k] != b[k] {
			return false
		}
	}
	return true
}

func mustOp(t *testing.T, comps []Comp) Op {
	t.Helper()
	op, err := normalize(comps)
	if err != nil {
		t.Fatalf("normalize(%v): %v", comps, err)
	}
	return op
}

// Example 1 from the spec: delete before a concurrent insert.
func TestExampleDeleteThenInsert(t *testing.T) {
	got := transform(mustOp(t, []Comp{r(2), d(2), r(1)}),
		mustOp(t, []Comp{r(3), i("X"), r(2)}))
	want := mustOp(t, []Comp{r(2), i("X"), r(1)})
	if !opsEqual(got, want) {
		t.Fatalf("got %s want %s", opString(got), opString(want))
	}
}

// Example 2: committed insert sits to the left of a concurrent insert.
func TestExampleConcurrentInserts(t *testing.T) {
	got := transform(mustOp(t, []Comp{r(1), i("P"), r(1)}),
		mustOp(t, []Comp{r(1), i("Q"), r(1)}))
	want := mustOp(t, []Comp{r(2), i("Q"), r(1)})
	if !opsEqual(got, want) {
		t.Fatalf("got %s want %s", opString(got), opString(want))
	}
}

// Example 3: identical deletes transform into an empty operation.
func TestExampleSameDeleteNoOp(t *testing.T) {
	got := transform(mustOp(t, []Comp{r(1), d(2), r(1)}),
		mustOp(t, []Comp{r(1), d(2), r(1)}))
	want := mustOp(t, []Comp{r(2)})
	if !opsEqual(got, want) {
		t.Fatalf("got %s want %s", opString(got), opString(want))
	}
	if !isNoOp(got) {
		t.Fatalf("expected no-op, got %s", opString(got))
	}
}

// Two overlapping deletes remove each rune only once.
func TestOverlappingDeletes(t *testing.T) {
	cases := []struct {
		s, c, want Op
	}{
		{
			mustOp(t, []Comp{d(3), r(3)}),
			mustOp(t, []Comp{r(2), d(2), r(2)}),
			mustOp(t, []Comp{d(1), r(2)}),
		},
		{
			mustOp(t, []Comp{r(1), d(3), r(1)}),
			mustOp(t, []Comp{r(2), d(1), r(2)}),
			mustOp(t, []Comp{r(2)}),
		},
		{
			mustOp(t, []Comp{r(2), d(2), r(2)}),
			mustOp(t, []Comp{r(1), d(3), r(2)}),
			mustOp(t, []Comp{r(1), d(1), r(2)}),
		},
	}
	for idx, tc := range cases {
		got := transform(tc.s, tc.c)
		if !opsEqual(got, tc.want) {
			t.Fatalf("case %d: got %s want %s", idx, opString(got), opString(tc.want))
		}
	}
}

// Multiple inserts landing inside one server delete survive at the deletion
// point; two clients' inserts keep client commit order at that point.
func TestInsertsInsideDeletedRegion(t *testing.T) {
	s := mustOp(t, []Comp{r(1), d(3), r(1)})
	c1 := mustOp(t, []Comp{r(2), i("A"), r(3)})
	c2 := mustOp(t, []Comp{r(3), i("B"), r(2)})

	doc, _ := New(1000)
	if _, err := doc.Submit("seed", 1, 0, mustOp(t, []Comp{i("abcde")})); err != nil {
		t.Fatal(err)
	}
	if _, err := doc.Submit("s", 1, 1, s); err != nil {
		t.Fatal(err)
	}
	if res, err := doc.Submit("a", 1, 1, c1); err != nil {
		t.Fatal(err)
	} else {
		want := mustOp(t, []Comp{r(1), i("A"), r(1)})
		if !opsEqual(res.Op, want) {
			t.Fatalf("c1 transformed = %s want %s", opString(res.Op), opString(want))
		}
	}
	if res, err := doc.Submit("b", 1, 1, c2); err != nil {
		t.Fatal(err)
	} else {
		want := mustOp(t, []Comp{r(2), i("B"), r(1)})
		if !opsEqual(res.Op, want) {
			t.Fatalf("c2 transformed = %s want %s", opString(res.Op), opString(want))
		}
	}
	// The deletion point sits between a and e; inserts keep commit order.
	if got := doc.Text(); got != "aABe" {
		t.Fatalf("document = %q want %q", got, "aABe")
	}
	if h, err := doc.History(2, 4); err != nil || len(h) != 2 {
		t.Fatalf("history = %v err=%v", h, err)
	}
}

// A no-op submission advances seq but not rev, and records its result.
func TestNoOpAdvancesSeqOnly(t *testing.T) {
	doc, _ := New(100)
	if _, err := doc.Submit("s", 1, 0, mustOp(t, []Comp{i("abcd")})); err != nil {
		t.Fatal(err)
	}
	op := mustOp(t, []Comp{r(1), d(2), r(1)})
	if _, err := doc.Submit("a", 1, 1, op); err != nil {
		t.Fatal(err)
	}
	if doc.Rev() != 2 {
		t.Fatalf("rev = %d want 2", doc.Rev())
	}
	res, err := doc.Submit("b", 1, 1, op)
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied || res.Rev != 2 || doc.Rev() != 2 {
		t.Fatalf("no-op changed revision: res=%+v rev=%d", res, doc.Rev())
	}
	if doc.Text() != "ad" {
		t.Fatalf("doc = %q want ad", doc.Text())
	}
	if _, err := doc.Submit("b", 3, 2, mustOp(t, []Comp{r(2)})); !errors.Is(err, ErrSeqGap) {
		t.Fatalf("seq gap: %v", err)
	}
	res2, err := doc.Submit("b", 2, 2, mustOp(t, []Comp{r(2), i("Z")}))
	if err != nil || !res2.Applied || res2.Rev != 3 {
		t.Fatalf("seq-2 submit: %+v %v", res2, err)
	}
}

// Duplicate submissions replay the recorded result without changing state.
func TestDuplicateReplay(t *testing.T) {
	doc, _ := New(100)
	op := mustOp(t, []Comp{i("hi")})
	res, err := doc.Submit("a", 1, 0, op)
	if err != nil {
		t.Fatal(err)
	}
	dup, err := doc.Submit("a", 1, 0, mustOp(t, []Comp{i("DIFFERENT")}))
	if err != nil {
		t.Fatal(err)
	}
	if dup.Rev != res.Rev || dup.Applied != res.Applied || !opsEqual(dup.Op, res.Op) {
		t.Fatalf("duplicate %+v != original %+v", dup, res)
	}
	if doc.Text() != "hi" || doc.Rev() != 1 {
		t.Fatalf("duplicate changed state: %q rev=%d", doc.Text(), doc.Rev())
	}
	dup.Op[0].Text = "xx"
	// A duplicate still carries the site's seq; invalid parameters take
	// priority over duplicate replay, so replay with a valid-looking op.
	again, err := doc.Submit("a", 1, 0, mustOp(t, []Comp{r(1)}))
	if err != nil {
		t.Fatal(err)
	}
	if again.Op[0].Text != "hi" {
		t.Fatalf("stored replay corrupted: %s", opString(again.Op))
	}
}

// MaxLen boundary: exactly equal passes, one rune over rejects, and rejection
// is measured on the transformed operation (both opposite constructs).
func TestMaxLenBoundary(t *testing.T) {
	doc, _ := New(3)
	if _, err := doc.Submit("a", 1, 0, mustOp(t, []Comp{i("abc")})); err != nil {
		t.Fatalf("exact fit rejected: %v", err)
	}
	if _, err := doc.Submit("a", 2, 1, mustOp(t, []Comp{r(3), i("X")})); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("one over: %v", err)
	}
	if doc.Rev() != 1 || doc.Text() != "abc" {
		t.Fatalf("rejected submit changed state: rev=%d doc=%q", doc.Rev(), doc.Text())
	}

	// Original op fits at its empty base, but a concurrent commit already
	// filled the document: transformed insert overflows.
	d1, _ := New(3)
	if _, err := d1.Submit("s", 1, 0, mustOp(t, []Comp{i("abc")})); err != nil {
		t.Fatal(err)
	}
	if _, err := d1.Submit("c", 1, 0, mustOp(t, []Comp{i("X")})); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("transformed overflow: %v", err)
	}

	// Reverse construct at a nonempty base: the original op grows the base
	// document past MaxLen, but the server deleted concurrently, so the
	// transformed result fits even though the original would not.
	d2, _ := New(4)
	if _, err := d2.Submit("s", 1, 0, mustOp(t, []Comp{i("abcd")})); err != nil {
		t.Fatal(err)
	}
	if _, err := d2.Submit("s", 2, 1, mustOp(t, []Comp{d(4)})); err != nil {
		t.Fatal(err)
	}
	res, err := d2.Submit("c", 1, 1, mustOp(t, []Comp{r(4), i("X")}))
	if err != nil {
		t.Fatalf("transformed fit rejected: %v", err)
	}
	if !res.Applied || res.Rev != 3 || d2.Text() != "X" {
		t.Fatalf("state = rev %d %q res %+v", d2.Rev(), d2.Text(), res)
	}
}

// floor / Compact boundary behavior.
func TestCompactFloor(t *testing.T) {
	doc, _ := New(1000)
	seq := 1
	submit := func(op Op) {
		t.Helper()
		if _, err := doc.Submit("s", seq, doc.Rev(), op); err != nil {
			t.Fatal(err)
		}
		seq++
	}
	submit(mustOp(t, []Comp{i("abc")}))           // rev 1
	submit(mustOp(t, []Comp{r(1), i("X"), r(2)})) // rev 2
	submit(mustOp(t, []Comp{r(2), d(1), r(1)}))   // rev 3

	if err := doc.Compact(2); err != nil {
		t.Fatal(err)
	}
	if doc.Floor() != 2 || doc.Rev() != 3 || doc.Text() != "aXc" {
		t.Fatalf("after compact: floor=%d rev=%d doc=%q", doc.Floor(), doc.Rev(), doc.Text())
	}
	// baseRev == floor is legal; below floor is ErrTooOld.
	if _, err := doc.Submit("s", seq, 2, mustOp(t, []Comp{r(4)})); err != nil {
		t.Fatalf("baseRev=floor: %v", err)
	}
	seq++
	if _, err := doc.Submit("s", seq, 1, mustOp(t, []Comp{r(4)})); !errors.Is(err, ErrTooOld) {
		t.Fatalf("below floor: %v", err)
	}
	if err := doc.Compact(1); !errors.Is(err, ErrBadFloor) {
		t.Fatalf("compact backwards: %v", err)
	}
	if err := doc.Compact(99); !errors.Is(err, ErrBadFloor) {
		t.Fatalf("compact past rev: %v", err)
	}

	h, err := doc.History(2, 3)
	if err != nil || len(h) != 1 {
		t.Fatalf("history = %v err=%v", h, err)
	}
	if want := mustOp(t, []Comp{r(2), d(1), r(1)}); !opsEqual(h[0], want) {
		t.Fatalf("history[0] = %s want %s", opString(h[0]), opString(want))
	}
	if _, err := doc.History(1, 3); !errors.Is(err, ErrTooOld) {
		t.Fatalf("history below floor: %v", err)
	}
	if _, err := doc.History(3, 2); !errors.Is(err, ErrBadRange) {
		t.Fatalf("from>to: %v", err)
	}
	if _, err := doc.History(2, 4); !errors.Is(err, ErrBadRange) {
		t.Fatalf("to>rev: %v", err)
	}

	// Recorded duplicate results survive compaction.
	before, err := doc.Submit("z", 1, 3, mustOp(t, []Comp{r(3), i("!")}))
	if err != nil {
		t.Fatal(err)
	}
	// At rev 4 the document length is still 4.
	if err := doc.Compact(4); err != nil {
		t.Fatal(err)
	}
	dup, err := doc.Submit("z", 1, 4, mustOp(t, []Comp{r(4)}))
	if err != nil || dup.Rev != before.Rev || dup.Applied != before.Applied || !opsEqual(dup.Op, before.Op) {
		t.Fatalf("duplicate after compact: %+v %v vs %+v", dup, err, before)
	}
}

// Error-priority and basic validation checks.
func TestErrorsAndPriority(t *testing.T) {
	doc, _ := New(10)
	if _, err := New(0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("maxLen=0: %v", err)
	}
	if _, err := New(1_000_001); !errors.Is(err, ErrInvalid) {
		t.Fatalf("maxLen too big: %v", err)
	}
	badR := []Comp{{Kind: Retain, N: 0}}
	if _, err := doc.Submit("", 1, 0, badR); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty site: %v", err)
	}
	if _, err := doc.Submit("a", 0, 0, badR); !errors.Is(err, ErrInvalid) {
		t.Fatalf("seq<1: %v", err)
	}
	if _, err := doc.Submit("a", 1, -1, badR); !errors.Is(err, ErrInvalid) {
		t.Fatalf("baseRev<0: %v", err)
	}
	if _, err := doc.Submit("a", 1, 0, badR); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad component: %v", err)
	}
	if _, err := doc.Submit("a", 1, 0, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty op: %v", err)
	}
	if _, err := doc.Submit("a", 1, 0, []Comp{{Kind: Insert, Text: "\xff"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid utf8: %v", err)
	}
	// Parameter invalidity beats future/base checks.
	if _, err := doc.Submit("a", 1, 5, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("priority param>future: %v", err)
	}
	// A valid op at seq 2 for a fresh site is a gap, even with future rev.
	valid := mustOp(t, []Comp{r(1)})
	if _, err := doc.Submit("a", 2, 5, valid); !errors.Is(err, ErrSeqGap) {
		t.Fatalf("gap beats future: %v", err)
	}
	// Fresh site must start at seq 1.
	if _, err := doc.Submit("b", 1, 1, Op{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty before future: %v", err)
	}
	if _, err := doc.Submit("b", 1, 1, valid); !errors.Is(err, ErrFuture) {
		t.Fatalf("future: %v", err)
	}
	if _, err := doc.Submit("b", 3, 0, badR); !errors.Is(err, ErrInvalid) {
		t.Fatalf("param beats gap: %v", err)
	}
	if _, err := doc.Submit("b", 3, 0, valid); !errors.Is(err, ErrSeqGap) {
		t.Fatalf("gap from rev0 start: %v", err)
	}
	// Stale seq after a success.
	if _, err := doc.Submit("c", 1, 0, mustOp(t, []Comp{i("x")})); err != nil {
		t.Fatal(err)
	}
	if _, err := doc.Submit("c", 0, 0, badR); !errors.Is(err, ErrInvalid) {
		t.Fatalf("param beats stale: %v", err)
	}
	if _, err := doc.Submit("c", 0, 0, valid); !errors.Is(err, ErrInvalid) {
		t.Fatalf("seq0 invalid: %v", err)
	}
	dupRes, err := doc.Submit("c", 1, 0, mustOp(t, []Comp{r(1)}))
	if err != nil || !dupRes.Applied || dupRes.Op[0].Text != "x" {
		t.Fatalf("dup replay = %+v %v", dupRes, err)
	}
	// Length mismatch, then too-old ordering requires a floor first.
	if _, err := doc.Submit("d", 1, 0, mustOp(t, []Comp{r(1)})); !errors.Is(err, ErrLength) {
		t.Fatalf("length mismatch on empty doc: %v", err)
	}
}

func TestNormalizeCanonical(t *testing.T) {
	got := mustOp(t, []Comp{i("a"), i("b"), r(1), r(1), d(1), i("c"), d(1)})
	want := Op{
		{Kind: Insert, Text: "ab"},
		{Kind: Retain, N: 2},
		{Kind: Insert, Text: "c"},
		{Kind: Delete, N: 2},
	}
	if !opsEqual(got, want) {
		t.Fatalf("got %s want %s", opString(got), opString(want))
	}
}
