package orset

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"strconv"
	"testing"
)

// The checked counter must not grow with the number of unrelated elements.
func TestLookupCostIndependentOfM(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		st, err := New(0, m+4)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < m; i++ {
			if _, err := st.Add("e" + strconv.Itoa(i)); err != nil {
				t.Fatal(err)
			}
		}
		for _, e := range []string{"x", "x"} {
			if _, err := st.Add(e); err != nil {
				t.Fatal(err)
			}
		}
		st.Contains("x")
		if got := st.checked; got > 3 {
			t.Fatalf("m=%d: Contains inspected %d entries, want <= 3", m, got)
		}
		if err := st.Remove("x"); err != nil {
			t.Fatal(err)
		}
		if got := st.checked; got > 3 {
			t.Fatalf("m=%d: Remove inspected %d entries, want <= 3", m, got)
		}
	}
}

// randState builds a deterministic pseudo-random state (adds and removes).
func randState(seed uint64, id int) *State {
	rng := rand.New(rand.NewPCG(seed, 9))
	st, _ := New(id, 1000)
	for i := 0; i < 20; i++ {
		e := "e" + strconv.Itoa(rng.IntN(6))
		if rng.IntN(4) == 0 {
			st.Remove(e)
		} else {
			st.Add(e)
		}
	}
	return st
}

func TestMergeLaws(t *testing.T) {
	eq := func(a, b *State) bool {
		return reflect.DeepEqual(a.adds, b.adds) && reflect.DeepEqual(a.tombs, b.tombs)
	}
	for _, seed := range []uint64{1, 2, 3} {
		sx, sy, sz := seed, seed+100, seed+200
		xy, yx := randState(sx, 0), randState(sy, 1)
		xy.Merge(randState(sy, 1))
		yx.Merge(randState(sx, 0))
		if !eq(xy, yx) {
			t.Fatal("merge not commutative")
		}
		l := randState(sx, 0)
		l.Merge(randState(sy, 1))
		l.Merge(randState(sz, 2))
		r := randState(sx, 0)
		yz := randState(sy, 1)
		yz.Merge(randState(sz, 2))
		r.Merge(yz)
		if !eq(l, r) {
			t.Fatal("merge not associative")
		}
		x1, x2 := randState(sx, 0), randState(sx, 0)
		x1.Merge(x2)
		if !eq(x1, x2) {
			t.Fatal("merge not idempotent")
		}
		ab, abb := randState(sx, 0), randState(sx, 0)
		ab.Merge(randState(sy, 1))
		abb.Merge(randState(sy, 1))
		abb.Merge(randState(sy, 1))
		if !eq(ab, abb) {
			t.Fatal("repeated merge changed the result")
		}
	}
}

func TestFailuresLeaveNoTrace(t *testing.T) {
	st, _ := New(0, 2)
	st.Add("a")
	st.Add("b") // now full: 2 add records
	full, _ := New(1, 4)
	full.Add("c")
	snap := fmt.Sprint(st.adds, st.tombs, st.next)
	ops := []struct {
		name string
		op   func() error
		want error
	}{
		{"empty-add", func() error { _, err := st.Add(""); return err }, ErrEmptyElement},
		{"remove-missing", func() error { return st.Remove("zz") }, ErrNotFound},
		{"add-over-capacity", func() error { _, err := st.Add("c"); return err }, ErrCapacity},
		{"merge-over-capacity", func() error { return st.Merge(full) }, ErrCapacity},
	}
	for _, tc := range ops {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.op(); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if got := fmt.Sprint(st.adds, st.tombs, st.next); got != snap {
				t.Fatalf("rejected op changed state: %s", got)
			}
		})
	}
}
