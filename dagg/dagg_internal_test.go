package dagg

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/mset"
)

// TestCheckedCountConstant pins the section-4 complexity claim: for a
// single-change batch, inspected (Group,Val) entries stay within a small
// constant independent of the group's distinct size m.
func TestCheckedCountConstant(t *testing.T) {
	const c = 2 // checked <= c * len(batch); here len(batch) == 1
	for _, m := range []int{100, 1000, 10000} {
		a := New(m * 2)
		seed := make([]Change, m)
		for i := range seed {
			seed[i] = C("g", fmt.Sprintf("v%05d", i), 1)
		}
		if _, err := a.Feed(seed); err != nil {
			t.Fatalf("m=%d seed: %v", m, err)
		}
		cases := []struct {
			name string
			ch   Change
			undo Change
		}{
			{"insert-new", C("g", "zz_new", 1), C("g", "zz_new", -1)},
			{"insert-existing", C("g", "v00000", 1), C("g", "v00000", -1)},
			{"withdraw-mult-one", C("g", "v00001", -1), C("g", "v00001", 1)},
		}
		for _, tc := range cases {
			if _, err := a.Feed([]Change{tc.ch}); err != nil {
				t.Fatalf("m=%d %s: %v", m, tc.name, err)
			}
			if a.checked > c {
				t.Errorf("m=%d %s: checked=%d, want <= %d (O(1) in m)", m, tc.name, a.checked, c)
			}
			_, _ = a.Feed([]Change{tc.undo}) // restore seed state
		}
	}
}

func TestNineBatches(t *testing.T) {
	B := [][]Change{{C("g", "a", 1)}, {C("g", "b", 1)}, {C("g", "a", 1)}, {C("g", "a", -1)},
		{C("g", "b", -1)}, {C("g", "b", -1)}, {C("g", "c", 1), C("g", "c", -1)},
		{C("g", "a", -1)}, {C("g", "a", 1)}}
	want := []string{"[{1 g 1}]", "[{-1 g 1} {1 g 2}]", "[]", "[]",
		"[{-1 g 2} {1 g 1}]", "REJECT", "[]", "[{-1 g 1}]", "[{1 g 1}]"}
	a := New(10)
	for i, b := range B {
		out, err := a.Feed(b)
		if want[i] == "REJECT" {
			if !errors.Is(err, ErrWithdrawAbsent) {
				t.Fatalf("batch %d: want ErrWithdrawAbsent, got %v", i+1, err)
			}
			continue
		}
		if err != nil || fmt.Sprint(out) != want[i] {
			t.Fatalf("batch %d: out=%v err=%v want %s", i+1, out, err, want[i])
		}
	}
	if a.View()["g"] != 1 || len(a.Log()) != 7 {
		t.Fatalf("final view=%v log=%d, want g:1 and 7 entries", a.View(), len(a.Log()))
	}
	a2 := New(10) // reversed batch 7: rejected, no trace
	for i := 0; i < 5; i++ {
		_, _ = a2.Feed(B[i])
	}
	before := fmt.Sprint(a2.View())
	if _, err := a2.Feed([]Change{C("g", "c", -1), C("g", "c", 1)}); !errors.Is(err, ErrWithdrawAbsent) {
		t.Fatalf("reversed batch 7: %v", err)
	}
	if fmt.Sprint(a2.View()) != before {
		t.Fatalf("reversed batch 7 left trace: %v", a2.View())
	}
}

func TestMultiplicityNonNegative(t *testing.T) { // invariant 3
	rng := rand.New(rand.NewSource(9))
	for _, vals := range []int{1, 50} {
		s, live := mset.New(), map[string]int{}
		for n := 0; n < 1000; n++ {
			v := fmt.Sprintf("v%d", rng.Intn(vals))
			if rng.Intn(2) == 0 {
				if s.Add(v) {
					live[v] = 0
				}
				live[v]++
			} else if cross, ok := s.Remove(v); !ok {
				if live[v] != 0 {
					t.Fatalf("false reject for live %s", v)
				}
			} else {
				live[v]--
				if cross {
					delete(live, v)
				}
			}
			if s.Mult(v) < 0 || s.Len() != len(live) || s.Distinct() != len(live) {
				t.Fatalf("step %d: negative mult or zero entry lingers (Len=%d live=%d)",
					n, s.Len(), len(live))
			}
		}
	}
}

func TestViewMatchesRecompute(t *testing.T) { // invariant 1
	for _, seed := range []int64{1, 2, 3} {
		rng := rand.New(rand.NewSource(seed))
		e, mult := New(1<<20), map[[2]string]int{}
		for n := 0; n < 500; n++ {
			b := make([]Change, 1+rng.Intn(5))
			for i := range b {
				b[i] = C(fmt.Sprintf("g%d", rng.Intn(4)), fmt.Sprintf("v%d", rng.Intn(8)), 1-2*rng.Intn(2))
			}
			if _, err := e.Feed(b); err != nil {
				continue
			}
			for _, c := range b {
				k := [2]string{c.Group, c.Val}
				if mult[k] += c.Sign; mult[k] == 0 {
					delete(mult, k)
				}
			}
			want := map[string]int{}
			for k, x := range mult {
				if x > 0 {
					want[k[0]]++
				}
			}
			if fmt.Sprint(e.View()) != fmt.Sprint(want) {
				t.Fatalf("seed %d step %d: view=%v want=%v", seed, n, e.View(), want)
			}
		}
	}
}
