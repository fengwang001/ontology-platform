package api_test

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
	"ontology/sm"
)

func fold(b int, cs []sm.Cmd) int {
	for _, c := range cs {
		b = sm.Apply(c, b)
	}
	return b
}
func ok(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}
func trip(a *api.API) [3]int { return [3]int{a.Committed(), a.LastApplied(), a.State()} }
func ready() *api.API {
	a := api.New()
	for _, c := range []sm.Cmd{sm.Add(2), sm.Mul(3), sm.Add(1), sm.Add(5)} {
		_ = a.Append(c)
	}
	return a
}
func TestRecomputeConsistency(t *testing.T) {
	for seed := int64(0); seed < 20; seed++ {
		a := api.New()
		var log []sm.Cmd
		snap, base := 0, 0
		rng := rand.New(rand.NewSource(seed))
		for step := 0; step < 100; step++ {
			switch rng.Intn(5) {
			case 0:
				c := []sm.Cmd{sm.Add(rng.Intn(5) + 1), sm.Mul(rng.Intn(3) + 1)}[rng.Intn(2)]
				ok(t, a.Append(c))
				log = append(log, c)
			case 1:
				_ = a.Commit(rng.Intn(len(log) + 1))
			case 2, 4:
				a.Apply()
			case 3:
				if a.Committed() > 0 {
					i := rng.Intn(a.Committed() + 1)
					st := fold(0, log[:i])
					_ = a.Restart(i, st)
					snap, base = i, st
				}
			}
			if want := fold(base, log[snap:a.LastApplied()]); a.State() != want {
				t.Fatalf("seed=%d step=%d got %d want %d", seed, step, a.State(), want)
			}
		}
	}
}
func TestConvergence(t *testing.T) {
	for _, tc := range []struct{ n, snap int }{{1, 0}, {4, 2}, {20, 7}, {50, 13}, {100, 50}} {
		cmds := make([]sm.Cmd, tc.n)
		for k := range cmds {
			cmds[k] = sm.Add(k + 1)
			if k%3 == 1 {
				cmds[k] = sm.Mul(2)
			}
		}
		mk := func() *api.API {
			b := api.New()
			for _, c := range cmds {
				_ = b.Append(c)
			}
			return b
		}
		one, many := mk(), mk()
		base := fold(0, cmds[:tc.snap])
		_ = one.Commit(tc.snap)
		_ = many.Commit(tc.snap)
		ok(t, one.Restart(tc.snap, base))
		ok(t, many.Restart(tc.snap, base))
		_ = one.Commit(tc.n)
		one.Apply()
		for i := tc.snap + 1; i <= tc.n; i++ {
			_ = many.Commit(i)
			many.Apply()
		}
		if one.State() != many.State() || one.LastApplied() != many.LastApplied() {
			t.Fatalf("n=%d snap=%d: %d/%d != %d/%d",
				tc.n, tc.snap, one.State(), one.LastApplied(), many.State(), many.LastApplied())
		}
	}
}
func TestFiveStepScenario(t *testing.T) {
	a := ready()
	want := [][3]int{{0, 0, 0}, {3, 0, 0}, {3, 3, 7}, {4, 2, 6}, {4, 4, 12}}
	chk := func(i int) {
		if trip(a) != want[i] {
			t.Fatalf("S%d: got %v want %v", i+1, trip(a), want[i])
		}
	}
	chk(0)
	ok(t, a.Commit(3))
	chk(1)
	a.Apply()
	chk(2)
	ok(t, a.Restart(2, 6))
	ok(t, a.Commit(4))
	chk(3)
	a.Apply()
	chk(4)
}
func TestConcurrentReaders(t *testing.T) {
	a := ready()
	_ = a.Commit(4)
	a.Apply()
	const n = 64
	ch := make(chan [3]int, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() { defer wg.Done(); ch <- trip(a) }()
	}
	wg.Wait()
	close(ch)
	for got := range ch {
		if got != [3]int{4, 4, 12} {
			t.Fatalf("reader saw %v, want [4 4 12]", got)
		}
	}
}
func TestSelfCheck(t *testing.T) {
	a := api.New()
	_ = a.Append(sm.Add(3))
	_ = a.Commit(1)
	a.Apply()
	before := trip(a)
	ok(t, a.SelfCheck())
	if trip(a) != before {
		t.Fatalf("SelfCheck mutated receiver: before=%v after=%v", before, trip(a))
	}
	if !errors.Is(api.New().Append(sm.Cmd{}), api.ErrEmptyCommand) {
		t.Fatal("empty command must map to ErrEmptyCommand")
	}
}
