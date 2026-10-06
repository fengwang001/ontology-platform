package fedalloc

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// TestOrderIndependence: shuffled registration order must not change targets.
func TestOrderIndependence(t *testing.T) {
	l := newLogger(t)
	defer l.flush()
	base := []Cluster{
		{Name: "a", Weight: 3, Capacity: 50, MinReplicas: 2, Available: true, CurrentReplicas: 4},
		{Name: "b", Weight: 5, Capacity: 7, Available: true, CurrentReplicas: 6},
		{Name: "c", Weight: 2, Capacity: 9, Available: true},
		{Name: "d", Weight: 0, MinReplicas: 1, Capacity: 3, Available: true, CurrentReplicas: 1},
	}
	run := func(cs []Cluster) []Target {
		rr := NewRegistry()
		for _, c := range cs {
			if err := rr.Upsert(c); err != nil {
				t.Fatal(err)
			}
		}
		res, err := rr.Allocate(42)
		if err != nil {
			t.Fatal(err)
		}
		return res.Targets
	}
	ref := run(base)
	shuffled := append([]Cluster(nil), base...)
	rand.New(rand.NewSource(7)).Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})
	l.log("reference targets=%v", ref)
	got := run(shuffled)
	l.log("shuffled  targets=%v", got)
	l.check(fmt.Sprint(ref) == fmt.Sprint(got), "identical targets regardless of input order")
}

// TestErrorPriority verifies the high-to-low error precedence.
func TestErrorPriority(t *testing.T) {
	l := newLogger(t)
	defer l.flush()
	r := NewRegistry()
	l.expectAllocError(r, -1, KindInvalidArgument)
	l.expectUpsertError(r, Cluster{Name: "bad", Weight: -1, Available: true}, KindInvalidArgument)
	l.expectUpsertError(r,
		Cluster{Name: "bad2", MinReplicas: 5, MaxReplicas: ptr64(3), Capacity: 100, Available: true},
		KindInvalidArgument)

	// config conflict AND min>total simultaneously -> config conflict wins.
	r2 := NewRegistry()
	l.upsert(r2, Cluster{Name: "a", Weight: 1, MinReplicas: 100, Capacity: 2, Available: true})
	l.expectAllocError(r2, 5, KindConfigConflict)

	// min-sum > total takes precedence over (potential) capacity shortage.
	r3 := NewRegistry()
	l.upsert(r3, Cluster{Name: "a", Weight: 1, MinReplicas: 10, Capacity: 12, Available: true})
	l.expectAllocError(r3, 5, KindMinExceedsTotal)

	// same-name upsert updates instead of duplicating.
	l.upsert(r3, Cluster{Name: "a", Weight: 2, MinReplicas: 10, Capacity: 12, Available: true})
	l.check(len(r3.Snapshot()) == 1, "same-name upsert updates rather than duplicates")

	l.log("DELETE input=\"\" expect invalid")
	err := r.Delete("")
	l.log("DELETE output err=%v", err)
	l.check(err != nil && err.Kind() == KindInvalidArgument, "empty-name delete is invalid")
	l.log("DELETE input=missing expect invalid")
	err = r.Delete("missing")
	l.log("DELETE output err=%v", err)
	l.check(err != nil && err.Kind() == KindInvalidArgument, "missing-name delete is invalid")
}

func TestDeleteTombstoneEvacuation(t *testing.T) {
	l := newLogger(t)
	defer l.flush()
	r := NewRegistry()
	l.upsert(r, Cluster{Name: "gone", Weight: 1, Capacity: 100, CurrentReplicas: 4, Available: true})
	l.upsert(r, Cluster{Name: "stay", Weight: 1, Capacity: 100, CurrentReplicas: 4, Available: true})
	l.log("DELETE input=gone")
	err := r.Delete("gone")
	l.log("DELETE output err=%v", err)
	l.check(err == nil, "delete succeeds")
	res := l.alloc(r, 8)
	m := targetsMap(res)
	l.check(m["gone"] == 0 && m["stay"] == 8, "tombstoned cluster evacuated into stay: %v", m)
	l.check(res.TotalMigration == 4, "migration 4 from deleted cluster")

	// re-registration clears the tombstone.
	l.upsert(r, Cluster{Name: "gone", Weight: 1, Capacity: 100, Available: true})
	res2 := l.alloc(r, 8)
	m2 := targetsMap(res2)
	l.check(m2["gone"] == 4 && m2["stay"] == 4, "reregistered gone participates again: %v", m2)
}

// TestUnchangedClustersOmitted: target == current clusters stay out of plan.
func TestUnchangedClustersOmitted(t *testing.T) {
	l := newLogger(t)
	defer l.flush()
	r := NewRegistry()
	l.upsert(r, Cluster{Name: "same", Weight: 5, Capacity: 100, CurrentReplicas: 5, Available: true})
	l.upsert(r, Cluster{Name: "grow", Weight: 1, Capacity: 100, CurrentReplicas: 0, Available: true})
	res := l.alloc(r, 6)
	for _, ch := range res.Plan {
		l.check(ch.Name != "same", "'same' must not appear in plan; got %+v", res.Plan)
	}
	l.check(len(res.Plan) == 1 && res.Plan[0].Name == "grow" && res.Plan[0].Target == 1,
		"only grow is planned: %+v", res.Plan)
}

// TestCurrentLoadOnlyBreaksFractionTies: change loads without changing the
// fractional situation and confirm targets are stable; then force a tie and
// confirm load flips exactly the +1 recipient.
func TestCurrentLoadOnlyBreaksFractionTies(t *testing.T) {
	l := newLogger(t)
	defer l.flush()

	// No fractional tie (weights 1 and 2, remainder 7 -> fractions 1/3 vs 2/3).
	// Arbitrarily changing current loads must not change targets.
	loads := [][]int64{{0, 0}, {5, 100}, {100, 0}}
	var ref map[string]int64
	for i, ld := range loads {
		r := NewRegistry()
		l.upsert(r, Cluster{Name: "a", Weight: 1, Capacity: 1000, Available: true, CurrentReplicas: ld[0]})
		l.upsert(r, Cluster{Name: "b", Weight: 2, Capacity: 1000, Available: true, CurrentReplicas: ld[1]})
		res := l.alloc(r, 7)
		m := targetsMap(res)
		l.log("load scenario %d => %v", i, m)
		if ref == nil {
			ref = m
		} else {
			l.check(m["a"] == ref["a"] && m["b"] == ref["b"],
				"targets invariant to current load when fractions differ: %v vs %v", m, ref)
		}
	}

	// Exact fractional tie: the higher load receives the single bonus.
	r := NewRegistry()
	l.upsert(r, Cluster{Name: "lo", Weight: 1, Capacity: 100, Available: true, CurrentReplicas: 1})
	l.upsert(r, Cluster{Name: "hi", Weight: 1, Capacity: 100, Available: true, CurrentReplicas: 50})
	res := l.alloc(r, 3)
	m := targetsMap(res)
	l.check(m["lo"] == 1 && m["hi"] == 2, "load breaks exact tie in favour of hi: %v", m)
}

// TestConcurrency exercises parallel registry mutations and atomic
// allocate-and-commit; committed target sums always equal the request and
// every allocation sees a consistent snapshot.
func TestConcurrency(t *testing.T) {
	l := newLogger(t)
	defer l.flush()
	r := NewRegistry()
	for i := 0; i < 8; i++ {
		if err := r.Upsert(Cluster{
			Name:      fmt.Sprintf("c%d", i),
			Weight:    int64(1 + i),
			Capacity:  1000,
			Available: true,
		}); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	failures := []string{}
	addFail := func(s string) {
		mu.Lock()
		failures = append(failures, s)
		mu.Unlock()
	}

	// mutators: flip availability and adjust weights.
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				name := fmt.Sprintf("c%d", (g+k)%8)
				snap := r.Snapshot()
				var cur Cluster
				found := false
				for _, c := range snap {
					if c.Name == name {
						cur, found = c, true
						break
					}
				}
				if !found {
					continue
				}
				cur.Available = !cur.Available
				if err := r.Upsert(cur); err != nil {
					addFail(err.Error())
				}
			}
		}(g)
	}

	// allocators: plan-only calls must never mutate state.
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				res, err := r.Allocate(int64(50 + (k % 200)))
				if err != nil {
					continue // rejection is acceptable while cluster is down
				}
				sum := int64(0)
				for _, tg := range res.Targets {
					sum += tg.Replicas
				}
				if sum != res.Total {
					addFail(fmt.Sprintf("plan sum %d != total %d", sum, res.Total))
				}
			}
		}()
	}

	// committer goroutines using the atomic primitive.
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				res, err := r.AllocateAndCommit(64)
				if err != nil {
					continue
				}
				sum := int64(0)
				for _, tg := range res.Targets {
					sum += tg.Replicas
				}
				if sum != 64 {
					addFail(fmt.Sprintf("committed sum %d != 64", sum))
				}
			}
		}()
	}

	wg.Wait()
	l.log("concurrency failures=%v", failures)
	l.check(len(failures) == 0, "no inconsistent allocations under concurrency: %v", failures)

	// Final committed state: snapshot targets sum consistency for one request.
	res, err := r.Allocate(64)
	if err == nil {
		sum := int64(0)
		for _, tg := range res.Targets {
			sum += tg.Replicas
		}
		l.check(sum == 64, "final consistent snapshot sums to request")
	}
}
