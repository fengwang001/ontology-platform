package query

import (
	"errors"
	"sort"
	"testing"

	"ontology/ival"
	"ontology/tree"
)

func iv(l, r int64) ival.Interval { return ival.Interval{L: l, R: r} }

func sortedKey(a []ival.Interval) []ival.Interval {
	out := append([]ival.Interval(nil), a...)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].L < out[j].L || out[i].L == out[j].L && out[i].R < out[j].R
	})
	return out
}

func TestStabAndOverlap(t *testing.T) {
	cases := []struct {
		name string
		data []ival.Interval
		q    ival.Interval
		want []ival.Interval
	}{
		{
			"abutting not overlap",
			[]ival.Interval{iv(1, 3), iv(3, 5)}, iv(2, 4),
			[]ival.Interval{iv(1, 3), iv(3, 5)},
		},
		{
			"touch boundary only",
			[]ival.Interval{iv(1, 3), iv(3, 5)}, iv(3, 3),
			nil,
		},
		{
			"containment",
			[]ival.Interval{iv(1, 9), iv(3, 5), iv(10, 12)}, iv(4, 6),
			[]ival.Interval{iv(1, 9), iv(3, 5)},
		},
		{
			"multiset identical",
			[]ival.Interval{iv(2, 4), iv(2, 4), iv(2, 4)}, iv(0, 10),
			[]ival.Interval{iv(2, 4), iv(2, 4), iv(2, 4)},
		},
		{
			"empty tree",
			nil, iv(0, 1),
			nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr := tree.New()
			for _, x := range c.data {
				if err := tr.Insert(x); err != nil {
					t.Fatal(err)
				}
			}
			e := New(tr)
			got, err := e.Overlap(c.q)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(c.want) {
				t.Fatalf("got %v want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("pos %d got %v want %v", i, got[i], c.want[i])
				}
			}
		})
	}
}

func TestStabBoundaryOwnership(t *testing.T) {
	tr := tree.New()
	for _, x := range []ival.Interval{iv(1, 3), iv(3, 5)} {
		if err := tr.Insert(x); err != nil {
			t.Fatal(err)
		}
	}
	e := New(tr)
	cases := []struct {
		x    int64
		want ival.Interval
	}{
		{2, iv(1, 3)},
		{3, iv(3, 5)},
		{5, iv(0, 0)}, // 右开，5 不命中任何
		{0, iv(0, 0)},
	}
	for _, c := range cases {
		got := e.Stab(c.x)
		if c.want == (ival.Interval{}) {
			if len(got) != 0 {
				t.Fatalf("x=%d got %v want none", c.x, got)
			}
			continue
		}
		if len(got) != 1 || got[0] != c.want {
			t.Fatalf("x=%d got %v want [%v]", c.x, got, c.want)
		}
	}
}

func TestZeroLength(t *testing.T) {
	tr := tree.New()
	for _, x := range []ival.Interval{iv(2, 2), iv(1, 5), iv(0, 0)} {
		if err := tr.Insert(x); err != nil {
			t.Fatal(err)
		}
	}
	e := New(tr)
	if got := e.Stab(2); len(got) != 1 || got[0] != iv(1, 5) {
		t.Fatalf("stab 2 = %v, zero-length must not be hit", got)
	}
	got, err := e.Overlap(iv(1, 5))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != iv(1, 5) {
		t.Fatalf("overlap [1,5) = %v, zero-length must not appear", got)
	}
	got, err = e.Overlap(iv(2, 2))
	if err != nil || len(got) != 0 {
		t.Fatalf("overlap by zero-length q = %v, %v", got, err)
	}
	if tr.Len() != 3 {
		t.Fatalf("zero-length intervals still occupy slots: len=%d", tr.Len())
	}
}

func TestDeleteSemantics(t *testing.T) {
	tr := tree.New()
	e := New(tr)
	for k := 0; k < 3; k++ {
		_ = tr.Insert(iv(2, 4))
	}
	_ = tr.Insert(iv(5, 6))
	if err := tr.Delete(iv(2, 4)); err != nil {
		t.Fatal(err)
	}
	got, _ := e.Overlap(iv(0, 10))
	if len(got) != 3 { // 两个副本 + [5,6)
		t.Fatalf("after one delete got %d results", len(got))
	}
	if err := tr.Delete(iv(5, 6)); err != nil {
		t.Fatal(err)
	}
	got, _ = e.Overlap(iv(5, 6))
	if len(got) != 0 {
		t.Fatalf("deleted interval still queried: %v", got)
	}
	if err := tr.Delete(iv(9, 9)); !errors.Is(err, tree.ErrNotFound) {
		t.Fatalf("missing delete: %v", err)
	}
}

func TestBatchLimit(t *testing.T) {
	tr := tree.New()
	_ = tr.Insert(iv(1, 2))
	e := New(tr, WithMaxBatch(2))
	if _, err := e.BatchOverlap([]ival.Interval{iv(0, 3), iv(0, 3)}); err != nil {
		t.Fatalf("within limit: %v", err)
	}
	_, err := e.BatchOverlap([]ival.Interval{iv(0, 3), iv(0, 3), iv(0, 3)})
	if !errors.Is(err, ErrBatchLimit) {
		t.Fatalf("got %v want ErrBatchLimit", err)
	}
	var le *BatchLimitError
	if !errors.As(err, &le) || le.Limit != 2 {
		t.Fatalf("limit detail: %v", err)
	}
	if _, err := e.Overlap(iv(0, 3)); err != nil {
		t.Fatalf("engine must remain usable: %v", err)
	}
}
