package tree_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"ontology/ival"
	"ontology/tree"
)

func iv(l, r int64) ival.Interval { return ival.Interval{L: l, R: r} }

func TestInsertAndSelfCheck(t *testing.T) {
	cases := []struct {
		name string
		list []ival.Interval
	}{
		{"empty", nil},
		{"ascending", []ival.Interval{iv(1, 2), iv(3, 4), iv(5, 6), iv(7, 8), iv(9, 10)}},
		{"descending", []ival.Interval{iv(9, 10), iv(7, 8), iv(5, 6), iv(3, 4), iv(1, 2)}},
		{"with dupes", []ival.Interval{iv(2, 4), iv(2, 4), iv(1, 3), iv(2, 4), iv(1, 3)}},
		{"zero length", []ival.Interval{iv(2, 2), iv(0, 5), iv(9, 9)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr := tree.New()
			for _, x := range c.list {
				if err := tr.Insert(x); err != nil {
					t.Fatalf("insert %v: %v", x, err)
				}
			}
			if err := tr.SelfCheck(); err != nil {
				t.Fatalf("selfcheck: %v", err)
			}
			if tr.Len() != len(c.list) {
				t.Fatalf("len=%d want %d", tr.Len(), len(c.list))
			}
		})
	}
}

func TestErrorsAndNoStateChange(t *testing.T) {
	tr := tree.New()
	if err := tr.Insert(iv(5, 1)); !errors.Is(err, ival.ErrInvalidInterval) {
		t.Fatalf("invalid insert err=%v", err)
	}
	if tr.Len() != 0 {
		t.Fatalf("state changed after invalid insert: len=%d", tr.Len())
	}
	if err := tr.Delete(iv(1, 2)); !errors.Is(err, tree.ErrNotFound) {
		t.Fatalf("delete missing err=%v", err)
	}
	limited := tree.New(tree.WithMaxIntervals(2))
	if err := limited.Insert(iv(1, 2)); err != nil {
		t.Fatal(err)
	}
	if err := limited.Insert(iv(3, 4)); err != nil {
		t.Fatal(err)
	}
	err := limited.Insert(iv(5, 6))
	if !errors.Is(err, tree.ErrLimitExceeded) {
		t.Fatalf("limit err=%v", err)
	}
	if limited.Len() != 2 {
		t.Fatalf("len=%d after rejected insert", limited.Len())
	}
	if err := limited.SelfCheck(); err != nil {
		t.Fatalf("tree unusable after reject: %v", err)
	}
	if err := limited.Delete(iv(1, 2)); err != nil {
		t.Fatal(err)
	}
	if err := limited.Insert(iv(7, 8)); err != nil {
		t.Fatalf("tree must remain usable: %v", err)
	}
}

func TestDeleteMultiset(t *testing.T) {
	tr := tree.New()
	for k := 0; k < 3; k++ {
		if err := tr.Insert(iv(2, 4)); err != nil {
			t.Fatal(err)
		}
	}
	for want := 2; want >= 0; want-- {
		if got := len(tr.Overlap(iv(0, 10), nil)); got != want+1 {
			t.Fatalf("overlap count=%d want %d", got, want+1)
		}
		if err := tr.Delete(iv(2, 4)); err != nil {
			t.Fatal(err)
		}
		if err := tr.SelfCheck(); err != nil {
			t.Fatalf("selfcheck after delete: %v", err)
		}
	}
	if err := tr.Delete(iv(2, 4)); !errors.Is(err, tree.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// TestRandomMatchesNaive 用固定种子随机序列，逐操作比对树查询与朴素扫描。
func TestRandomMatchesNaive(t *testing.T) {
	for _, seed := range []int64{1, 42, 100} {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			tr := tree.New()
			var all []ival.Interval
			naiveOverlap := func(q ival.Interval) []ival.Interval {
				var out []ival.Interval
				for _, x := range all {
					if x.Overlaps(q) {
						out = append(out, x)
					}
				}
				sort.SliceStable(out, func(i, j int) bool {
					return out[i].L < out[j].L || (out[i].L == out[j].L && out[i].R < out[j].R)
				})
				return out
			}
			naiveStab := func(x int64) []ival.Interval {
				var out []ival.Interval
				for _, iv := range all {
					if iv.Contains(x) {
						out = append(out, iv)
					}
				}
				sort.SliceStable(out, func(i, j int) bool {
					return out[i].L < out[j].L || (out[i].L == out[j].L && out[i].R < out[j].R)
				})
				return out
			}
			check := func(q ival.Interval) {
				t.Helper()
				if err := tr.SelfCheck(); err != nil {
					t.Fatal(err)
				}
				gotO := tr.Overlap(q, nil)
				wantO := naiveOverlap(q)
				if !equalSlices(gotO, wantO) {
					t.Fatalf("overlap %v: got %v want %v", q, gotO, wantO)
				}
				gotS := tr.Stab(q.L+1, nil)
				wantS := naiveStab(q.L + 1)
				if !equalSlices(gotS, wantS) {
					t.Fatalf("stab: got %v want %v", gotS, wantS)
				}
			}
			for k := 0; k < 400; k++ {
				l := rng.Int63n(40)
				r := l + rng.Int63n(10)
				x := iv(l, r)
				if rng.Intn(3) == 0 && len(all) > 0 {
					victim := all[rng.Intn(len(all))]
					if err := tr.Delete(victim); err != nil {
						t.Fatal(err)
					}
					all = removeFirst(all, victim)
				} else {
					if err := tr.Insert(x); err != nil {
						t.Fatal(err)
					}
					all = append(all, x)
				}
				check(iv(rng.Int63n(45), rng.Int63n(45)+1))
			}
		})
	}
}

func equalSlices(a, b []ival.Interval) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func removeFirst(s []ival.Interval, v ival.Interval) []ival.Interval {
	for i := range s {
		if s[i] == v {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}
