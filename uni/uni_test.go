package uni

import (
	"fmt"
	"math/rand"
	"testing"
)

// TestProbeConstantInM: "a" held by all m partitions; removing copies
// must inspect an m-independent number of partitions. The last Remove
// (cnt 1->0, emits "-a") is covered too.
func TestProbeConstantInM(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		u := New(m)
		for p := 0; p < m; p++ {
			u.Add(p, "a")
		}
		u.Remove(0, "a") // cnt m -> m-1, no changelog
		if u.lastProbe > 1 {
			t.Fatalf("m=%d: first Remove inspected %d partitions", m, u.lastProbe)
		}
		if n := len(u.Changes()); n != 1 { // only the fill's "+a"
			t.Fatalf("m=%d: unexpected changelog entries %d", m, n)
		}
		for p := 1; p < m; p++ {
			_, emitted := u.Remove(p, "a")
			last := p == m-1
			if emitted != last { // only the final cnt 1->0 emits
				t.Fatalf("m=%d p=%d: emitted=%v want %v", m, p, emitted, last)
			}
			if u.lastProbe > 1 {
				t.Fatalf("m=%d p=%d: Remove inspected %d partitions", m, p, u.lastProbe)
			}
		}
		if got := u.View(); len(got) != 0 {
			t.Fatalf("m=%d: view not empty: %v", m, got)
		}
		if !ProbeBounded(m) {
			t.Fatalf("m=%d: ProbeBounded false", m)
		}
	}
}

// TestRandomSelfCheck drives random sequences and asks SelfCheck to
// verify the four invariants against an independent per-partition model.
func TestRandomSelfCheck(t *testing.T) {
	for _, seed := range []int64{3, 17, 88} {
		rng := rand.New(rand.NewSource(seed))
		n := 5
		u := New(n)
		model := make([]map[string]struct{}, n)
		for i := range model {
			model[i] = map[string]struct{}{}
		}
		for i := 0; i < 600; i++ {
			p, e := rng.Intn(n), fmt.Sprintf("e%d", rng.Intn(6))
			if rng.Intn(2) == 0 {
				u.Add(p, e)
				model[p][e] = struct{}{}
			} else {
				u.Remove(p, e)
				delete(model[p], e)
			}
			if err := u.SelfCheck(model); err != nil {
				t.Fatalf("seed=%d op %d: %v", seed, i, err)
			}
		}
	}
}
