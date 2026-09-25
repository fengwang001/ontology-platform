package merge

import (
	"math/bits"
	"math/rand"
	"strconv"
	"testing"

	"ontology/msrc"
)

// TestMinHeadComparisonsSublinear is white-box: it reads the unexported
// cmpCount directly (same package). m one-event sources with distinct TS are
// registered in random order; after one watermark step the head-comparison
// count must stay within 4*log2(m)+4, i.e. must not grow linearly with m.
func TestMinHeadComparisonsSublinear(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		m := m
		t.Run(strconv.Itoa(m), func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(m)))
			ts := rng.Perm(m) // distinct TS, random arrival order
			mr := New()
			for i := 0; i < m; i++ {
				nm := "s" + strconv.Itoa(i)
				e := []msrc.Event{{Src: nm, Seq: 0, TS: int64(ts[i]) + 1, Key: "k"}}
				if err := mr.Add(nm, e); err != nil {
					t.Fatalf("add: %v", err)
				}
			}
			mr.ensureHeap()
			if !mr.step() {
				t.Fatal("expected at least one step")
			}
			bound := 4*bits.Len(uint(m)) + 4
			if mr.cmpCount > bound {
				t.Fatalf("m=%d comparisons=%d exceed 4*log2(m)+4=%d (linear?)", m, mr.cmpCount, bound)
			}
			if m >= 1000 && mr.cmpCount >= m/2 {
				t.Fatalf("m=%d comparisons=%d look linear", m, mr.cmpCount)
			}
		})
	}
}

// TestStepOrderIsTotalOrder verifies retained events come out in ≺ order and
// dedup still works when one source has an equal-TS run in one round.
func TestStepOrderIsTotalOrder(t *testing.T) {
	src := map[string][]msrc.Event{
		"A": {{Seq: 0, TS: 5, Key: "k", Val: "a0"}, {Seq: 1, TS: 5, Key: "j", Val: "a1"}},
		"B": {{Seq: 0, TS: 5, Key: "k", Val: "b0"}},
	}
	mr := New()
	for name, evs := range src {
		if err := mr.Add(name, evs); err != nil {
			t.Fatalf("add %s: %v", name, err)
		}
	}
	log := mr.Drain()
	for i := 1; i < len(log); i++ {
		if lessTotal(log[i], log[i-1]) {
			t.Fatalf("log not in ≺ order at %d", i)
		}
	}
	if mr.Dups() != 1 {
		t.Fatalf("dups = %d, want 1", mr.Dups())
	}
}

func lessTotal(a, b msrc.Event) bool {
	return a.TS < b.TS || a.TS == b.TS && (a.Src < b.Src || a.Src == b.Src && a.Seq < b.Seq)
}
