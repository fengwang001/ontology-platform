package dagg_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
	"ontology/dagg"
)

func TestChangelogSelfConsistent(t *testing.T) { // invariant 2
	rng := rand.New(rand.NewSource(7))
	e := dagg.New(1 << 20)
	held := map[string]int{} // downstream materialized view
	for n := 0; n < 400; n++ {
		b := []dagg.Change{dagg.C(fmt.Sprintf("g%d", rng.Intn(3)), fmt.Sprintf("v%d", rng.Intn(5)), 1-2*rng.Intn(2))}
		out, err := e.Feed(b)
		if err != nil {
			continue
		}
		for i, o := range out {
			switch o.Sign {
			case -1:
				if held[o.Group] != o.N {
					t.Fatalf("step %d: '-' withdraws %d, held %d", n, o.N, held[o.Group])
				}
				delete(held, o.Group)
			case 1:
				if _, dup := held[o.Group]; dup {
					t.Fatalf("step %d: two values held for %s", n, o.Group)
				}
				if i > 0 && out[i-1].Group == o.Group && out[i-1].Sign == -1 && out[i-1].N == o.N {
					t.Fatalf("step %d: equal -(G,n) +(G,%d) pair", n, o.N)
				}
				held[o.Group] = o.N
			}
		}
		if fmt.Sprint(held) != fmt.Sprint(e.View()) {
			t.Fatalf("step %d: downstream %v != view %v", n, held, e.View())
		}
	}
}

func TestRejectedBatchNoTrace(t *testing.T) { // invariant 4
	C := dagg.C
	cases := []struct {
		name string
		max  int
		pre  []dagg.Change
		bad  []dagg.Change
		want error
	}{
		{"withdraw-absent", 10, nil, []dagg.Change{C("g", "a", -1)}, dagg.ErrWithdrawAbsent},
		{"bad-sign", 10, nil, []dagg.Change{C("g", "a", 0)}, dagg.ErrInvalidChange},
		{"empty-group", 10, nil, []dagg.Change{C("", "a", 1)}, dagg.ErrInvalidChange},
		{"empty-val", 10, nil, []dagg.Change{C("g", "", 1)}, dagg.ErrInvalidChange},
		{"limit-exceeded", 1, []dagg.Change{C("g", "a", 1)}, []dagg.Change{C("g", "b", 1)}, dagg.ErrLimitExceeded},
		{"mid-batch-withdraw", 10, []dagg.Change{C("g", "a", 1)}, []dagg.Change{C("g", "b", 1), C("g", "b", -1), C("g", "b", -1)}, dagg.ErrWithdrawAbsent},
	}
	for _, tc := range cases {
		e := dagg.New(tc.max)
		if tc.pre != nil {
			if _, err := e.Feed(tc.pre); err != nil {
				t.Fatalf("%s: pre: %v", tc.name, err)
			}
		}
		v0, l0 := fmt.Sprint(e.View()), len(e.Log())
		if _, err := e.Feed(tc.bad); !errors.Is(err, tc.want) {
			t.Fatalf("%s: want %v, got %v", tc.name, tc.want, err)
		}
		if fmt.Sprint(e.View()) != v0 || len(e.Log()) != l0 {
			t.Fatalf("%s: rejected batch left trace view=%v (was %s)", tc.name, e.View(), v0)
		}
		if _, err := e.Feed([]dagg.Change{C("g", "a", 1)}); err != nil { // still usable
			t.Fatalf("%s: engine unusable after reject: %v", tc.name, err)
		}
	}
	if dagg.ErrWithdrawAbsent == dagg.ErrInvalidChange || dagg.ErrInvalidChange == dagg.ErrLimitExceeded ||
		dagg.ErrWithdrawAbsent == dagg.ErrLimitExceeded {
		t.Fatal("the three sentinel errors are not distinct")
	}
}

func TestConcurrency(t *testing.T) {
	e := dagg.New(1 << 20)
	seed := make([]dagg.Change, 300)
	for i := range seed {
		seed[i] = dagg.C("g", fmt.Sprintf("v%d", i), 1)
	}
	if _, err := e.Feed(seed); err != nil {
		t.Fatal(err)
	}
	const N = 16
	var wg sync.WaitGroup
	snaps := make([]string, N)
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) { defer wg.Done(); snaps[i] = fmt.Sprint(e.View()) }(i)
	}
	wg.Wait()
	for _, s := range snaps[1:] {
		if s != snaps[0] {
			t.Fatalf("reader saw %s, want %s", s, snaps[0])
		}
	}
	e2 := dagg.New(1 << 20) // disjoint-group writers == batch recompute
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			g := fmt.Sprintf("grp%02d", i)
			_, _ = e2.Feed([]dagg.Change{dagg.C(g, "x", 1), dagg.C(g, "y", 1)})
		}(i)
	}
	wg.Wait()
	if len(e2.View()) != N {
		t.Fatalf("writers view=%v, want %d groups", e2.View(), N)
	}
	if err := api.New(10).SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
