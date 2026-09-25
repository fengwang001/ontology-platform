package reg

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// TestRefCountConsistency drives random op sequences and checks the
// count always equals the number of distinct holders in a model.
func TestRefCountConsistency(t *testing.T) {
	for seed := int64(0); seed < 5; seed++ {
		rng := rand.New(rand.NewSource(seed))
		r := New()
		model := map[string]map[string]bool{}
		for i := 0; i < 2000; i++ {
			id := fmt.Sprintf("o%d", rng.Intn(8))
			ref := fmt.Sprintf("r%d", rng.Intn(6))
			held, ok := model[id]
			var err, want error
			switch rng.Intn(3) {
			case 0:
				err = r.Create(id)
				if ok {
					want = ErrExists
				} else {
					model[id] = map[string]bool{}
				}
			case 1:
				err = r.Acquire(ref, id)
				if !ok {
					want = ErrNotFound
				} else {
					held[ref] = true
				}
			case 2:
				err = r.Release(ref, id)
				switch {
				case !ok:
					want = ErrNotFound
				case !held[ref]:
					want = ErrNotHeld
				default:
					delete(held, ref)
					if len(held) == 0 {
						delete(model, id) // reclaimed at zero
					}
				}
			}
			if !errors.Is(err, want) {
				t.Fatalf("seed%d op%d: err=%v want %v", seed, i, err, want)
			}
			for id, hs := range model {
				if n, err := r.RefCount(id); err != nil || n != len(hs) || !r.Alive(id) {
					t.Fatalf("seed%d: %s n=%d err=%v want %d", seed, id, n, err, len(hs))
				}
			}
		}
	}
}

// TestReclaimTiming: reclaimed exactly on 1->0, never above, gone after.
func TestReclaimTiming(t *testing.T) {
	r := New()
	_ = r.Create("X")
	_ = r.Acquire("a", "X")
	_ = r.Acquire("b", "X")
	if err := r.Release("a", "X"); err != nil || !r.Alive("X") {
		t.Fatal("must survive at count 1")
	}
	if err := r.Release("b", "X"); err != nil || r.Alive("X") {
		t.Fatal("must be reclaimed at count 0")
	}
	for _, err := range []error{r.Acquire("c", "X"), r.Release("c", "X")} {
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("op on reclaimed = %v, want ErrNotFound", err)
		}
	}
	if _, err := r.RefCount("X"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("RefCount on reclaimed = %v", err)
	}
}

// TestIdempotentShared: duplicate acquire counts once; only the last
// release of a shared object reclaims it.
func TestIdempotentShared(t *testing.T) {
	r := New()
	_ = r.Create("X")
	for i := 0; i < 5; i++ {
		_ = r.Acquire("a", "X")
	}
	if n, _ := r.RefCount("X"); n != 1 {
		t.Fatalf("5 dup acquires -> count %d, want 1", n)
	}
	_ = r.Acquire("b", "X")
	_ = r.Release("a", "X")
	if !r.Alive("X") {
		t.Fatal("shared object reclaimed too early")
	}
	_ = r.Release("b", "X")
	if r.Alive("X") {
		t.Fatal("last release must reclaim")
	}
}

// TestFailureNoSideEffect: every rejected op class leaves state intact.
func TestFailureNoSideEffect(t *testing.T) {
	cases := []struct {
		op   func(r *Registry) error
		want error
	}{
		{func(r *Registry) error { return r.Create("") }, ErrEmpty},
		{func(r *Registry) error { return r.Acquire("", "Y") }, ErrEmpty},
		{func(r *Registry) error { return r.Create("Y") }, ErrExists},
		{func(r *Registry) error { return r.Acquire("b", "ZZ") }, ErrNotFound},
		{func(r *Registry) error { return r.Release("a", "ZZ") }, ErrNotFound},
		{func(r *Registry) error { return r.Release("b", "Y") }, ErrNotHeld},
	}
	for i, tc := range cases {
		r := New()
		_ = r.Create("Y")
		_ = r.Acquire("a", "Y")
		if err := tc.op(r); !errors.Is(err, tc.want) {
			t.Fatalf("case%d: err=%v want %v", i, err, tc.want)
		}
		if n, _ := r.RefCount("Y"); n != 1 || !r.Alive("Y") {
			t.Fatalf("case%d: rejected op mutated state", i)
		}
		if err := r.Release("a", "Y"); err != nil {
			t.Fatalf("case%d: unusable after rejection", i)
		}
	}
}

// TestReclaimCheckCost: reclamation examines O(1) objects regardless
// of registry size m.
func TestReclaimCheckCost(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		r := New()
		for i := 0; i < m; i++ {
			id := fmt.Sprintf("o%d", i)
			_ = r.Create(id)
			_ = r.Acquire(fmt.Sprintf("r%d", i), id)
		}
		if err := r.Release("r0", "o0"); err != nil || r.lastChecks > 1 {
			t.Fatalf("m=%d: checked %d objects err=%v, want checks<=1", m, r.lastChecks, err)
		}
	}
}
