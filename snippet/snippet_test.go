package snippet

import (
	"errors"
	"reflect"
	"testing"
)

func mustRegister(t *testing.T, r *Registry, id, text string) {
	t.Helper()
	if err := r.Register(id, text); err != nil {
		t.Fatalf("Register(%q) unexpected error: %v", id, err)
	}
}

func rejectReason(t *testing.T, err error, want RejectReason) {
	t.Helper()
	var re *RejectError
	if !errors.As(err, &re) {
		t.Fatalf("want *RejectError %s, got %v", want, err)
	}
	if re.Reason != want {
		t.Fatalf("want reason %s, got %s", want, re.Reason)
	}
}

// e==a+W is included; a hit ending at a+W+1 is not.
func TestWindowEdgeInclusion(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "d", "abcdefghij")
	got, err := r.Snippets("d", []Interval{{0, 5}, {0, 6}}, 5, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := []Snippet{{Start: 0, End: 5, Highlights: []Interval{{0, 5}}, Score: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

// Equal scores at distinct starts take the smallest a.
func TestTieSmallestStart(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "d", "abcdefghijklmnop")
	got, err := r.Snippets("d", []Interval{{1, 2}, {8, 9}}, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Start != 1 || got[1].Start != 8 || got[0].Score != 1 || got[1].Score != 1 {
		t.Fatalf("unexpected: %+v", got)
	}
}

// Overlapping distinct hits each score; highlights merge when s2 < e1.
func TestOverlappingHitsScoreAndMerge(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "d", "abcdefghij")
	got, err := r.Snippets("d", []Interval{{0, 5}, {2, 7}}, 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := []Snippet{{Start: 0, End: 7, Highlights: []Interval{{0, 7}}, Score: 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

// Touching [1,3) and [3,5) stay separate in highlights.
func TestTouchingHighlightsNotMerged(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "d", "abcdefghij")
	got, err := r.Snippets("d", []Interval{{1, 3}, {3, 5}}, 8, 1)
	if err != nil {
		t.Fatal(err)
	}
	wantHL := []Interval{{1, 3}, {3, 5}}
	if !reflect.DeepEqual(got[0].Highlights, wantHL) {
		t.Fatalf("got %+v want %+v", got[0].Highlights, wantHL)
	}
}

// A hit longer than W fits no window; max score 0 ends immediately.
func TestHitLongerThanWindowEnds(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "d", "abcdefghij")
	got, err := r.Snippets("d", []Interval{{0, 6}}, 5, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("want no snippets, got %+v", got)
	}
}

// The start of a window selected first (right side, higher score)
// truncates r(a) for a left-side candidate in the next round.
func TestWindowTruncatedBySelectedStart(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "d", "abcdefghijklmnopqrstuvwxyz012345")
	// Round 1: a=20, r=30 covers 6 hits ending <=28, beating a=15 which
	// covers only hits ending <=25; window becomes [20,28).
	// Round 2: spanning [15,24) intersects [20,28) and is gone; a=15 gets
	// r=min(25,32,20)=20 and keeps only [15,17).
	hits := []Interval{
		{20, 22}, {21, 23}, {22, 24}, {23, 26}, {24, 27}, {25, 28},
		{15, 17}, {15, 24},
	}
	got, err := r.Snippets("d", hits, 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 snippets, got %+v", got)
	}
	if !(got[0].Start == 15 && got[0].End == 17 && got[0].Score == 1) {
		t.Fatalf("truncated snippet wrong: %+v", got[0])
	}
	if !(got[1].Start == 20 && got[1].End == 28 && got[1].Score == 6) {
		t.Fatalf("right snippet wrong: %+v", got[1])
	}
}

// A hit spanning a selected window boundary intersects it and disappears.
func TestCrossBoundaryHitBecomesUnavailable(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "d", "abcdefghijklmnopqrstuvwxyz")
	// Round 1: a=0, [0,2),[1,3) fit W=3 -> score 2, window [0,3).
	// [2,8) intersects [0,3) (2<3 && 0<8) and is removed. Round 2 keeps
	// only [9,10).
	got, err := r.Snippets("d", []Interval{{0, 2}, {1, 3}, {2, 8}, {9, 10}}, 3, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Start != 0 || got[1].Start != 9 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Score != 2 {
		t.Fatalf("first window score wrong: %+v", got[0])
	}
}

// Output is Start-ascending even though a later start was selected first.
func TestOutputSortedByStartNotSelectionOrder(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "d", "abcdefghijklmnopqrstuvwxyz012345")
	hits := []Interval{{20, 21}, {21, 22}, {22, 23}, {0, 1}}
	got, err := r.Snippets("d", hits, 5, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Start != 0 || got[1].Start != 20 {
		t.Fatalf("want Start-ascending order, got %+v", got)
	}
}

// Endpoints inside a multi-byte rune reject the whole request.
func TestMultiByteBoundaryRejected(t *testing.T) {
	r := NewRegistry()
	text := "ab中cd" // 中 occupies byte offsets 2,3,4
	mustRegister(t, r, "d", text)
	for _, h := range []Interval{{2, 4}, {3, 5}, {2, 3}, {1, 4}, {3, 4}} {
		_, err := r.Snippets("d", []Interval{h}, 4, 1)
		if err == nil {
			t.Fatalf("hit %+v splitting rune was accepted", h)
		}
		rejectReason(t, err, ReasonInvalidHit)
	}
	// Boundary-aligned endpoints are accepted.
	if _, err := r.Snippets("d", []Interval{{0, 2}, {2, 5}, {5, 7}, {0, 7}}, 7, 1); err != nil {
		t.Fatalf("boundary-aligned hits rejected: %v", err)
	}
}

// K larger than the number of selectable snippets returns fewer.
func TestKLargerThanSelectable(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "d", "abcdefghij")
	got, err := r.Snippets("d", []Interval{{0, 1}, {5, 6}}, 2, 16)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 snippets, got %+v", got)
	}
}

func TestEmptyHits(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "d", "abc")
	got, err := r.Snippets("d", nil, 3, 1)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %+v err=%v", got, err)
	}
}

func TestRejectionDoesNotMutateState(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "keep", "abc")

	if err := r.Register("", "x"); err == nil {
		t.Fatal("empty docID accepted")
	} else {
		rejectReason(t, err, ReasonInvalidArgument)
	}
	if err := r.Register("bad", "abc\xff"); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	} else {
		rejectReason(t, err, ReasonInvalidArgument)
	}
	big := make([]byte, maxTextBytes+1)
	if err := r.Register("bad", string(big)); err == nil {
		t.Fatal("over-long text accepted")
	} else {
		rejectReason(t, err, ReasonInvalidArgument)
	}
	if err := r.Register("keep", "xyz"); err == nil {
		t.Fatal("duplicate docID accepted")
	} else {
		rejectReason(t, err, ReasonDuplicateDoc)
	}
	if err := r.Unregister("missing"); err == nil {
		t.Fatal("unregister unknown accepted")
	} else {
		rejectReason(t, err, ReasonDocNotFound)
	}

	if _, err := r.Snippets("ghost", nil, 1, 1); err == nil {
		t.Fatal("unknown doc accepted")
	} else {
		rejectReason(t, err, ReasonDocNotFound)
	}
	if _, err := r.Snippets("keep", nil, 0, 1); err == nil {
		t.Fatal("W=0 accepted")
	} else {
		rejectReason(t, err, ReasonInvalidArgument)
	}
	if _, err := r.Snippets("keep", nil, 4097, 1); err == nil {
		t.Fatal("W=4097 accepted")
	} else {
		rejectReason(t, err, ReasonInvalidArgument)
	}
	if _, err := r.Snippets("keep", nil, 1, 0); err == nil {
		t.Fatal("K=0 accepted")
	} else {
		rejectReason(t, err, ReasonInvalidArgument)
	}
	if _, err := r.Snippets("keep", nil, 1, 17); err == nil {
		t.Fatal("K=17 accepted")
	} else {
		rejectReason(t, err, ReasonInvalidArgument)
	}

	many := make([]Interval, maxHits+1)
	if _, err := r.Snippets("keep", many, 1, 1); err == nil {
		t.Fatal("10001 hits accepted")
	} else {
		rejectReason(t, err, ReasonInvalidArgument)
	}
	for _, h := range []Interval{{-1, 1}, {2, 4}, {1, 1}, {0, 100}, {0, 0}} {
		_, err := r.Snippets("keep", []Interval{h}, 1, 1)
		if err == nil {
			t.Fatalf("bad hit %+v accepted", h)
		}
		rejectReason(t, err, ReasonInvalidHit)
	}

	// State unchanged: keep still holds the original text.
	got, err := r.Snippets("keep", []Interval{{0, 3}}, 3, 1)
	if err != nil {
		t.Fatalf("state mutated after rejections: %v", err)
	}
	if len(got) != 1 || got[0].Start != 0 || got[0].End != 3 {
		t.Fatalf("state mutated: %+v", got)
	}

	if err := r.Unregister("keep"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Snippets("keep", nil, 1, 1); err == nil {
		t.Fatal("document still present after unregister")
	}
}

func TestRejectionPriority(t *testing.T) {
	r := NewRegistry()
	// Unknown doc beats invalid arguments.
	_, err := r.Snippets("nope", nil, 0, 0)
	rejectReason(t, err, ReasonDocNotFound)

	mustRegister(t, r, "d", "abc")
	// Invalid W beats invalid individual hits.
	_, err = r.Snippets("d", []Interval{{5, 6}}, 0, 1)
	rejectReason(t, err, ReasonInvalidArgument)
	// Over-10000 hits beats invalid individual hits.
	many := make([]Interval, maxHits+1)
	many[0] = Interval{Start: -1, End: -2}
	_, err = r.Snippets("d", many, 1, 1)
	rejectReason(t, err, ReasonInvalidArgument)
}

// Results are order-independent and mutating caller slices after the call
// cannot change returned content (no aliasing of internal state).
func TestNoAliasAndOrderIndependence(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "d", "abcdefghijklmnopqrstuvwxyz")
	hits := []Interval{{0, 3}, {2, 5}, {10, 12}, {10, 13}}
	got1, err := r.Snippets("d", hits, 8, 4)
	if err != nil {
		t.Fatal(err)
	}
	shuffled := []Interval{{10, 13}, {0, 3}, {10, 12}, {2, 5}}
	got2, err := r.Snippets("d", shuffled, 8, 4)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got1, got2) {
		t.Fatalf("order-dependent results: %+v vs %+v", got1, got2)
	}

	// Mutate the caller's hit slice and result slices; re-query and compare.
	hits[0] = Interval{Start: 0, End: 1}
	for i := range got1 {
		got1[i].Start = 999
		for j := range got1[i].Highlights {
			got1[i].Highlights[j] = Interval{Start: 999, End: 999}
		}
	}
	got3, err := r.Snippets("d", []Interval{{0, 3}, {2, 5}, {10, 12}, {10, 13}}, 8, 4)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(got3, got1) {
		t.Fatalf("returned snippets alias internal state: %+v", got3)
	}
}
