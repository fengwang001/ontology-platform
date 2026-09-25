package prune

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"ontology/part"
)

// D holds the five §3 fixtures, each as [lo, hi, min_v, max_v].
var D = [][4]int64{{0, 10, 10, 20}, {10, 20, 30, 40}, {20, 30, 50, 60}, {30, 40, 15, 25}, {40, 50, 60, 80}}
var q5 = Predicate{PLo: 20, PHi: 50, VLo: 30, VHi: 60}

func build(t *testing.T) *Table {
	t.Helper()
	x := NewTable()
	for i, z := range D {
		p, err := part.New(fmt.Sprintf("P%d", i), z[0], z[1], z[2], z[3])
		if err != nil || x.Add(p) != nil {
			t.Fatal(err)
		}
	}
	return x
}

// grid enumerates valid predicates over every equality-sensitive boundary.
func grid() (out []Predicate) {
	ps := []int64{-5, 0, 10, 20, 30, 40, 50, 60}
	vs := []int64{5, 15, 20, 25, 30, 40, 55, 60, 80}
	for _, a := range ps {
		for _, b := range ps {
			for _, c := range vs {
				for _, d := range vs {
					if a < b && c < d {
						out = append(out, Predicate{PLo: a, PHi: b, VLo: c, VHi: d})
					}
				}
			}
		}
	}
	return
}

// check recomputes expectations straight from the part primitives (no binary
// window) and returns soundness, exactness and order-independence for r.
func check(q Predicate, r Result) [3]bool {
	sound := true
	var staticFirst, dynamicFirst []string
	for i, z := range D {
		p, _ := part.New(fmt.Sprintf("P%d", i), z[0], z[1], z[2], z[3])
		sp := p.StaticPruned(q.PLo, q.PHi)
		dp := p.DynamicPruned(q.VLo, q.VHi)
		if !sp { // static-first: only p-survivors take the v test
			if !dp {
				staticFirst = append(staticFirst, p.ID())
			}
		}
		if !dp { // dynamic-first: only v-survivors take the p test
			if !sp {
				dynamicFirst = append(dynamicFirst, p.ID())
			}
		}
		if slices.Contains(r.Pruned, p.ID()) && !sp && !dp {
			sound = false // an intersecting box was pruned: rows could be missed
		}
	}
	return [3]bool{sound, slices.Equal(r.Scan, staticFirst), slices.Equal(staticFirst, dynamicFirst)}
}

// assertOverGrid runs every boundary predicate and asserts invariant `which`
// (0 soundness, 1 exactness, 2 order-independence).
func assertOverGrid(t *testing.T, which int) {
	t.Helper()
	x := build(t)
	for _, q := range grid() {
		r, err := x.Prune(x.IDs(), q)
		if err != nil {
			t.Fatal(err)
		}
		got := check(q, r)
		if !got[which] {
			t.Fatalf("invariant %d violated by %v", which+1, q)
		}
	}
}

func TestSoundness(t *testing.T)        { assertOverGrid(t, 0) } // invariant 1
func TestExactness(t *testing.T)        { assertOverGrid(t, 1) } // invariant 2
func TestOrderIndependent(t *testing.T) { assertOverGrid(t, 2) } // invariant 3

func TestExactnessFixture(t *testing.T) { // §3 worked example yields exactly {P2}
	if r, e := build(t).Prune([]string{"P0", "P1", "P2", "P3", "P4"}, q5); e != nil ||
		fmt.Sprint(r.Scan) != "[P2]" {
		t.Fatalf("fixture scan=%v err=%v", r.Scan, e)
	}
}

func TestRejectedOpsLeaveNoTrace(t *testing.T) { // invariant 4
	if _, e := part.New("X", 10, 5, 1, 2); !errors.Is(e, part.ErrInvalidPartition) {
		t.Fatal(e)
	}
	if errors.Is(part.ErrInvalidPartition, ErrInvalidPredicate) ||
		errors.Is(ErrInvalidPredicate, ErrUnknownPartition) ||
		errors.Is(part.ErrInvalidPartition, ErrUnknownPartition) {
		t.Fatal("sentinels must be pairwise distinct")
	}
	x := build(t)
	before := fmt.Sprint(x.IDs())
	for _, c := range []struct {
		arg  []string
		q    Predicate
		want error
	}{
		{[]string{"GHOST"}, q5, ErrUnknownPartition},
		{x.IDs(), Predicate{PLo: 5, PHi: 5, VLo: 0, VHi: 1}, ErrInvalidPredicate},
		{x.IDs(), Predicate{PLo: 0, PHi: 1, VLo: 9, VHi: 9}, ErrInvalidPredicate},
	} {
		if _, e := x.Prune(c.arg, c.q); !errors.Is(e, c.want) {
			t.Fatalf("got %v want %v", e, c.want)
		}
	}
	if fmt.Sprint(x.IDs()) != before {
		t.Fatal("state changed after rejection")
	}
	if r, e := x.Prune(x.IDs(), q5); e != nil || fmt.Sprint(r.Scan) != "[P2]" {
		t.Fatalf("unusable after rejection %v %v", r, e)
	}
}

func TestCheckedSublinear(t *testing.T) { // counter constant across N=100..10000
	first := -1
	for _, n := range []int{100, 1000, 10000} {
		x := NewTable()
		for i := 0; i < n; i++ {
			p, _ := part.New(fmt.Sprintf("P%05d", i), int64(i*10), int64(i*10+10), 0, 100)
			if x.Add(p) != nil {
				t.Fatal("add")
			}
		}
		e, err := x.evaluate(x.IDs(), Predicate{PLo: 15, PHi: 16, VLo: 0, VHi: 1000})
		if err != nil || e.checked > 3 || len(e.Scan) != 1 || (first != -1 && e.checked != first) {
			t.Fatalf("n=%d checked=%d scan=%d", n, e.checked, len(e.Scan))
		}
		first = e.checked
	}
}
