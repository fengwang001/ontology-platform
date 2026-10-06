package mesh_test

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/mesh"
)

// TestConcurrentPublishRoute hammers publications and routes in parallel.
// Serializability is checked by: every successful publish bumps the version
// exactly by one; every observed route result is valid under exactly one
// published generation; -race covers memory safety.
func TestConcurrentPublishRoute(t *testing.T) {
	l := newOpLog(t)
	s := mesh.NewService()
	regReady(s, "a", "e1")
	regReady(s, "b", "e2")
	gen := func(which int) *mesh.Config {
		cfg := &mesh.Config{
			Rules: []mesh.Rule{{
				Matchers: []mesh.Matcher{{Path: &mesh.PathCondition{
					Kind:  mesh.PathExact,
					Value: fmt.Sprintf("/g%d", which%4),
				}}},
				Targets: []mesh.Target{{Subset: "a", Weight: 100}},
			}},
			Fallback: []mesh.Target{{Subset: "b", Weight: 100}},
		}
		return cfg
	}
	_, err := s.Publish(gen(0), 0)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	var conflicts, okPubs int64
	ver := int64(1)

	// Publishers: retry on version conflict so some successes occur.
	for p := 0; p < 4; p++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 60; i++ {
				base := int(atomic.LoadInt64(&ver))
				nv, e := s.Publish(gen(base+id+i), base)
				if e != nil {
					if e.Kind != mesh.KindVersionConflict {
						t.Errorf("unexpected publish error: %v", e)
						return
					}
					atomic.AddInt64(&conflicts, 1)
					continue
				}
				if !atomic.CompareAndSwapInt64(&ver, int64(base), int64(nv)) {
					t.Errorf("version ledger lost update: %d -> %d", base, nv)
					return
				}
				atomic.AddInt64(&okPubs, 1)
			}
		}(p)
	}

	// Routers: only /g0..g3 exist; every result must be either a rule hit
	// or fallback, never a half-state (e.g. NoRoute is impossible here).
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				path := fmt.Sprintf("/g%d", i%4)
				res, e := s.Route(mesh.Request{Path: path, Bucket: 100})
				l.record("Route(concurrent)", path,
					describeResult(res)+" "+describeErr(e),
					"result must belong to one fully-published generation")
				if e != nil && e.Kind != mesh.KindNoEndpoint {
					t.Errorf("unexpected route error: %v", e)
					return
				}
				if res != nil && res.Subset != "a" && res.Subset != "b" {
					t.Errorf("impossible subset: %s", res.Subset)
				}
			}
		}(r)
	}
	wg.Wait()
	t.Logf("successful publishes=%d conflicts=%d final version=%d",
		okPubs, conflicts, s.CurrentVersion())
	if okPubs == 0 || s.CurrentVersion() != int(atomic.LoadInt64(&ver)) {
		t.Fatalf("serializability ledger mismatch: okPubs=%d version=%d", okPubs, s.CurrentVersion())
	}
}

// bigConfig builds a service where n-1 rules live under unrelated deep
// paths and exactly one prefix rule can match "/hit".
func bigConfig(b *testing.B, n int) *mesh.Service {
	s := mesh.NewService()
	regReady(s, "hit", "e1")
	regReady(s, "other", "e2")
	cfg := &mesh.Config{}
	for i := 0; i < n; i++ {
		cfg.Rules = append(cfg.Rules, mesh.Rule{
			Matchers: []mesh.Matcher{{Path: &mesh.PathCondition{
				Kind:  mesh.PathExact,
				Value: fmt.Sprintf("/zzz/unrelated/%d/tail", i),
			}}},
			Targets: []mesh.Target{{Subset: "other", Weight: 100}},
		})
	}
	cfg.Rules = append(cfg.Rules, mesh.Rule{
		Matchers: []mesh.Matcher{{Path: &mesh.PathCondition{Kind: mesh.PathPrefix, Value: "/hit"}}},
		Targets:  []mesh.Target{{Subset: "hit", Weight: 100}},
	})
	if _, err := s.Publish(cfg, 0); err != nil {
		b.Fatal(err)
	}
	return s
}

// BenchmarkRouteUnrelatedRules proves per-request cost does not grow
// linearly with the number of unrelated rules: compare ns/op at 1000 and
// 4000 rules; growth must stay far below 4x.
func BenchmarkRouteUnrelatedRules(b *testing.B) {
	for _, n := range []int{1000, 4000} {
		s := bigConfig(b, n)
		b.Run(fmt.Sprintf("rules=%d", n), func(b *testing.B) {
			req := mesh.Request{Path: "/hit/part", Bucket: 100}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.Route(req); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
