package sampler

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"sampler/report"
)

// TestAgainstNaive replays one deterministic random op sequence against both
// the sampler and the naive model, comparing every output, then checks the
// conservation invariant per (tenant,key): accepted == kept + summarized +
// pending. Replaying the same sequence must reproduce identical outputs.
func TestAgainstNaive(t *testing.T) {
	const n, m, w, kt, tmax = 3, 2, 500, 4, 3
	run := func(seed int64, log bool) map[string][3]uint64 {
		s := mustNew(t, n, m, w, kt, tmax)
		nv := newNaive(n, m, w, kt, tmax)
		rng := rand.New(rand.NewSource(seed))
		var now uint64
		stat := map[string][3]uint64{} // id -> {accepted, kept, summarized}
		for op := 0; op < 4000; op++ {
			tn := fmt.Sprintf("t%d", rng.Intn(5))
			key := fmt.Sprintf("k%d", rng.Intn(9))
			sev := rng.Intn(7) - 1 // -1..5: sometimes invalid
			id := tn + "/" + key
			switch rng.Intn(10) {
			case 0: // flush, sink fails ~1/4 of deliveries
				now += uint64(rng.Intn(3 * w))
				var gotA, gotB []report.Summary
				fail := rng.Intn(4) == 0
				sink := func(got *[]report.Summary) report.Sink {
					return func(sm report.Summary) error {
						*got = append(*got, sm)
						if fail {
							return fmt.Errorf("injected")
						}
						return nil
					}
				}
				na, ea := s.Flush(now, sink(&gotA))
				nb, eb := nv.flush(now, sink(&gotB))
				if na != nb || (ea == nil) != (eb == nil) || !reflect.DeepEqual(gotA, gotB) {
					t.Fatalf("flush@%d: sampler(%d,%v,%v) naive(%d,%v,%v)", op, na, ea, gotA, nb, eb, gotB)
				}
				for _, sm := range gotA[:na] {
					st := stat[sm.Tenant+"/"+sm.Key]
					st[2] += sm.Dropped
					stat[sm.Tenant+"/"+sm.Key] = st
				}
				if log && op%997 == 0 {
					t.Logf("op%d Flush(now=%d) -> n=%d err=%v sums=%v", op, now, na, ea, gotA)
				}
			default: // record
				now += uint64(rng.Intn(3 * w))
				ka, sa, ea := s.Record(now, tn, key, sev)
				kb, sb, eb := nv.record(now, tn, key, sev)
				if ka != kb || (ea == nil) != (eb == nil) || !reflect.DeepEqual(sa, sb) {
					t.Fatalf("op%d Record(%d,%s,%s,%d): sampler(%v,%v,%v) naive(%v,%v,%v)",
						op, now, tn, key, sev, ka, sa, ea, kb, sb, eb)
				}
				if ea == nil {
					st := stat[id]
					st[0]++
					if ka {
						st[1]++
					}
					for _, sm := range sa {
						st[2] += sm.Dropped
					}
					stat[id] = st
				}
				if log && op%499 == 0 {
					t.Logf("op%d Record(now=%d,%s,%s,sev=%d) -> kept=%v sums=%v err=%v", op, now, tn, key, sev, ka, sa, ea)
				}
			}
		}
		for id, st := range stat {
			tn, key := id[:2], id[3:]
			pending, _ := s.Pending(tn, key)
			if st[0] != st[1]+st[2]+pending {
				t.Fatalf("invariant %s: accepted=%d kept=%d summarized=%d pending=%d",
					id, st[0], st[1], st[2], pending)
			}
		}
		return stat
	}
	a := run(42, true)
	b := run(42, false)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("same input sequence must replay to identical results")
	}
	t.Logf("invariant holds for %d (tenant,key) pairs; replay deterministic", len(a))
}

// TestConcurrent: concurrent Record/Flush/Pending are race-free under -race.
func TestConcurrent(t *testing.T) {
	s := mustNew(t, 2, 3, 100, 8, 4)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			for i := 0; i < 500; i++ {
				now := uint64(rng.Intn(50)) // may be rejected: fine
				s.Record(now, fmt.Sprintf("t%d", g%3), fmt.Sprintf("k%d", rng.Intn(6)), rng.Intn(6))
				if i%50 == 0 {
					s.Flush(now, func(report.Summary) error { return nil })
				}
				s.Pending("t0", "k0")
			}
		}(g)
	}
	wg.Wait()
	t.Log("8 goroutines x 500 ops completed under lock; run with -race")
}
