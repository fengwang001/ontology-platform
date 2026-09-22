package query

import (
	"errors"
	"math/rand"
	"slices"
	"testing"

	"ontology/ival"
	"ontology/tree"
)

func iv(lo, hi int64) ival.Interval { return ival.Interval{Lo: lo, Hi: hi} }

func build(t *testing.T, ivs []ival.Interval) (*tree.Tree, *Querier) {
	t.Helper()
	tr := tree.New()
	for _, x := range ivs {
		if err := tr.Insert(x); err != nil {
			t.Fatal(err)
		}
	}
	return tr, New(tr)
}

func naiveOverlap(all []ival.Interval, q ival.Interval) []ival.Interval {
	var out []ival.Interval
	for _, x := range all {
		if ival.Overlaps(x, q) {
			out = append(out, x)
		}
	}
	slices.SortFunc(out, ival.Compare)
	return out
}

func naiveStab(all []ival.Interval, p int64) []ival.Interval {
	var out []ival.Interval
	for _, x := range all {
		if x.Contains(p) {
			out = append(out, x)
		}
	}
	slices.SortFunc(out, ival.Compare)
	return out
}

// TestVsNaive compares tree queries against a linear scan over many
// random rounds (fixed seed), including empty-tree and zero-length cases.
func TestVsNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for round := 0; round < 60; round++ {
		n := rng.Intn(200)
		all := make([]ival.Interval, 0, n)
		tr := tree.New()
		for i := 0; i < n; i++ {
			lo, hi := rng.Int63n(300), rng.Int63n(300)
			if lo > hi {
				lo, hi = hi, lo
			}
			x := ival.Interval{Lo: lo, Hi: hi, Payload: string(rune('a' + rng.Intn(4)))}
			if err := tr.Insert(x); err != nil {
				t.Fatal(err)
			}
			all = append(all, x)
		}
		q := New(tr)
		for k := 0; k < 10; k++ {
			lo, hi := rng.Int63n(300), rng.Int63n(300)
			if lo > hi {
				lo, hi = hi, lo
			}
			got, err := q.Overlap(ival.Interval{Lo: lo, Hi: hi})
			if err != nil {
				t.Fatal(err)
			}
			if want := naiveOverlap(all, ival.Interval{Lo: lo, Hi: hi}); !slices.Equal(got, want) {
				t.Fatalf("round %d overlap [%d,%d): got %v want %v", round, lo, hi, got, want)
			}
			p := rng.Int63n(300)
			if got, want := q.Stab(p), naiveStab(all, p); !slices.Equal(got, want) {
				t.Fatalf("round %d stab %d: got %v want %v", round, p, got, want)
			}
		}
		if err := tr.Check(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSemanticsAndErrors(t *testing.T) {
	tr, q := build(t, []ival.Interval{iv(1, 3), iv(3, 5), iv(2, 2)})
	cases := []struct {
		name string
		got  []ival.Interval
		want []ival.Interval
	}{
		{"touching-not-overlap", must(q.Overlap(iv(1, 3))), []ival.Interval{iv(1, 3)}},
		{"stab-at-touch-point", q.Stab(3), []ival.Interval{iv(3, 5)}},
		{"zero-length-not-stabbed", q.Stab(2), []ival.Interval{iv(1, 3)}},
		{"zero-length-not-overlapped", must(q.Overlap(iv(1, 5))), []ival.Interval{iv(1, 3), iv(3, 5)}},
		{"zero-length-query-matches-nothing", must(q.Overlap(iv(4, 4))), nil},
	}
	for _, c := range cases {
		if !slices.Equal(c.got, c.want) {
			t.Errorf("%s: got %v want %v", c.name, c.got, c.want)
		}
	}
	if got := q.Stab(100); got != nil {
		t.Errorf("miss stab: got %v want nil", got)
	}
	if _, err := q.Overlap(iv(9, 1)); !errors.Is(err, ival.ErrInvalid) {
		t.Errorf("invalid query interval: got %v", err)
	}
	if tr.Size() != 3 {
		t.Errorf("zero-length interval must count toward size, got %d", tr.Size())
	}
}

func must(ivs []ival.Interval, err error) []ival.Interval {
	if err != nil {
		panic(err)
	}
	return ivs
}

// TestOrderIndependent inserts the same multiset in different orders and
// requires bitwise-identical results, including duplicate intervals.
func TestOrderIndependent(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	base := []ival.Interval{iv(1, 3), iv(3, 5), iv(2, 9), iv(1, 3), iv(4, 4), iv(0, 100)}
	var ref []ival.Interval
	for trial := 0; trial < 8; trial++ {
		perm := rng.Perm(len(base))
		ivs := make([]ival.Interval, 0, len(base))
		for _, i := range perm {
			ivs = append(ivs, base[i])
		}
		_, q := build(t, ivs)
		got := must(q.Overlap(iv(0, 100)))
		if trial == 0 {
			ref = got
		} else if !slices.Equal(got, ref) {
			t.Fatalf("trial %d: order-dependent result %v want %v", trial, got, ref)
		}
	}
}

// TestMultisetDeleteViaQuery deletes one of three identical intervals and
// requires the remaining two to stay visible; after deleting all, none.
func TestMultisetDeleteViaQuery(t *testing.T) {
	tr, q := build(t, []ival.Interval{iv(1, 5), iv(1, 5), iv(1, 5)})
	if err := tr.Delete(iv(1, 5)); err != nil {
		t.Fatal(err)
	}
	if got := must(q.Overlap(iv(0, 9))); len(got) != 2 {
		t.Fatalf("after one delete got %d results want 2", len(got))
	}
	_ = tr.Delete(iv(1, 5))
	_ = tr.Delete(iv(1, 5))
	if got := must(q.Overlap(iv(0, 9))); len(got) != 0 {
		t.Fatalf("deleted intervals must not appear, got %v", got)
	}
}
