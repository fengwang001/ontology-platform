package ring

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/change"
)

func TestRandomDeliveryOrdersConverge(t *testing.T) {
	for n := 3; n <= 8; n++ {
		t.Run(fmt.Sprintf("N=%d", n), func(t *testing.T) {
			for seed := int64(1); seed <= 20; seed++ {
				rng := rand.New(rand.NewSource(seed))
				r, err := New(n, allWriters(n))
				if err != nil {
					t.Fatal(err)
				}
				for site := 1; site <= n; site++ {
					for item := 0; item < 3; item++ {
						key := []byte(fmt.Sprintf("k%d", rng.Intn(4)))
						var op change.Op = change.Put{Value: int64(site*10 + item)}
						if item == 2 {
							op = change.Del
						}
						if _, err := r.Write(site, key, op, int64(site+item)); err != nil {
							t.Fatal(err)
						}
					}
				}

				dequeued, dup := drainRandom(t, r, rng)
				assertConvergence(t, r)
				writes := int64(3 * n)
				if int64(dequeued) != int64(n+1)*writes {
					t.Fatalf("seed=%d dequeued=%d want=%d", seed, dequeued, int64(n+1)*writes)
				}
				if int64(dup) != 2*writes {
					t.Fatalf("seed=%d dup=%d want=%d", seed, dup, 2*writes)
				}
				for from := 1; from <= n; from++ {
					for _, to := range neighbors(n, from) {
						if pending, err := r.Pending(from, to); err != nil || len(pending) != 0 {
							t.Fatalf("seed=%d link %d->%d pending=%d err=%v", seed, from, to, len(pending), err)
						}
					}
				}
			}
		})
	}
}

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	const n = 5
	r, err := New(n, allWriters(n))
	if err != nil {
		t.Fatal(err)
	}
	var writers sync.WaitGroup
	for site := 1; site <= n; site++ {
		writers.Add(1)
		go func(site int) {
			defer writers.Done()
			for item := 0; item < 20; item++ {
				now := int64(item * 10)
				op := change.Op(change.Put{Value: int64(site*100 + item)})
				if item%7 == 0 {
					op = change.Del
				}
				_, _ = r.Write(site, []byte(fmt.Sprintf("shared-%d", item%4)), op, now)
			}
		}(site)
	}

	var deliverers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		deliverers.Add(1)
		go func() {
			defer deliverers.Done()
			for attempt := 0; attempt < 200; attempt++ {
				from := 1 + attempt%n
				to := neighbors(n, from)[attempt%2]
				_, _ = r.Deliver(from, to)
			}
		}()
	}
	writers.Wait()
	deliverers.Wait()

	drainAll(t, r)
	assertConvergence(t, r)
}

func allWriters(n int) []int {
	writers := make([]int, n)
	for site := range writers {
		writers[site] = site + 1
	}
	return writers
}

func drainRandom(t *testing.T, r *Ring, rng *rand.Rand) (dequeued int, dup int) {
	t.Helper()
	n := r.n
	for {
		type edge struct{ from, to int }
		edges := make([]edge, 0, 2*n)
		for from := 1; from <= n; from++ {
			for _, to := range neighbors(n, from) {
				pending, err := r.Pending(from, to)
				if err != nil {
					t.Fatal(err)
				}
				if len(pending) > 0 {
					edges = append(edges, edge{from: from, to: to})
				}
			}
		}
		if len(edges) == 0 {
			return dequeued, dup
		}
		chosen := edges[rng.Intn(len(edges))]
		outcome, err := r.Deliver(chosen.from, chosen.to)
		if err != nil {
			t.Fatal(err)
		}
		dequeued++
		if outcome == DeliverDup {
			dup++
		}
	}
}
