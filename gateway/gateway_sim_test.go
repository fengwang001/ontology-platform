package gateway_test

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"ontology/gateway"
)

// TestNaiveDifferential runs random non-decreasing-timestamp scripts against
// both the real gateway and the naive model, logging input, output and the
// decision basis for every operation.
func TestNaiveDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(1349))
	for iter := 0; iter < 40; iter++ {
		const cap = int64(60)
		g, _ := gateway.New(cap)
		names := []string{"A", "B", "C", "D"}
		m := &naiveModel{cap: cap, tenants: map[string]*naiveTenant{}}
		specs := []struct {
			name       string
			rate, burs int64
			maxP       int
		}{{"A", 10, 60, 1}, {"B", 30, 60, 0}, {"C", 0, 30, 2}, {"D", 50, 40, 0}}
		for _, s := range specs {
			if err := g.AddTenant(s.name, s.rate, s.burs, s.maxP); err != nil {
				t.Fatal(err)
			}
			m.tenants[s.name] = &naiveTenant{rate: s.rate, cap: s.burs * 1000,
				tokens: s.burs * 1000, maxPrio: s.maxP}
		}

		var now int64
		for step := 0; step < 300; step++ {
			now += int64(rng.Intn(5))
			name := names[rng.Intn(4)]
			prio := rng.Intn(3)
			size := int64(1 + rng.Intn(int(cap)))
			wantEv, wantWhy := m.ingest(now, name, prio, size)
			res, err := g.Ingest(now, name, prio, size)
			gotWhy := "ok"
			if err != nil {
				gotWhy = errClass(err)
			}
			t.Logf("ingest step=%d input=(now=%d t=%s prio=%d size=%d) -> "+
				"got=%s(%d evicted) want=%s(%d evicted)",
				step, now, name, prio, size, gotWhy, len(res.Evicted),
				wantWhy, len(wantEv))
			if gotWhy != wantWhy {
				t.Fatalf("step %d: %s != %s", step, gotWhy, wantWhy)
			}
			if gotWhy == "ok" {
				if len(res.Evicted) != len(wantEv) {
					t.Fatalf("step %d: eviction length %d != %d",
						step, len(res.Evicted), len(wantEv))
				}
				for i, r := range res.Evicted {
					if r.Tenant != wantEv[i].tenant || r.Size != wantEv[i].size ||
						r.Prio != wantEv[i].prio {
						t.Fatalf("step %d eviction %d mismatch", step, i)
					}
				}
				assertModelMatch(t, g, m, step)
			}
			if rng.Intn(3) == 0 {
				budget := int64(rng.Intn(int(cap) + 1))
				wantD := m.drain(now, budget)
				gotD, derr := g.Drain(now, budget)
				t.Logf("drain  step=%d input=(now=%d budget=%d) -> %d records (want %d)",
					step, now, budget, len(gotD), len(wantD))
				if derr != nil {
					t.Fatalf("drain: %v", derr)
				}
				if len(gotD) != len(wantD) {
					t.Fatalf("step %d drain len %d != %d", step, len(gotD), len(wantD))
				}
				for i, r := range gotD {
					if r.Tenant != wantD[i].tenant || r.Size != wantD[i].size ||
						r.Prio != wantD[i].prio || r.Seq != wantD[i].seq {
						t.Fatalf("step %d drain %d mismatch", step, i)
					}
				}
				assertModelMatch(t, g, m, step)
			}
		}
	}
}

func errClass(err error) string {
	switch {
	case err == gateway.ErrQueueFull:
		return "full"
	case err == gateway.ErrQuota:
		return "quota"
	case err == gateway.ErrForbidden:
		return "forbidden"
	case err == gateway.ErrNoTenant:
		return "no-tenant"
	case err == gateway.ErrClockBackward:
		return "clock"
	default:
		return "invalid"
	}
}

func assertModelMatch(t *testing.T, g *gateway.Gateway, m *naiveModel, step int) {
	t.Helper()
	if g.Used() != m.used {
		t.Fatalf("step %d used %d != naive %d", step, g.Used(), m.used)
	}
	gs := g.Stats()
	var names []string
	for name := range gs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		nt := m.tenants[name]
		for p := 0; p < 3; p++ {
			got := gs[name]
			if got.AcceptedBytes[p] != nt.acc[p] ||
				got.EvictedBytes[p] != nt.evi[p] ||
				got.DrainedBytes[p] != nt.drn[p] ||
				got.QueuedBytes[p] != nt.que[p] {
				t.Fatalf("step %d %s prio %d stats mismatch: %+v vs naive "+
					"acc=%d evi=%d drn=%d que=%d",
					step, name, p, got, nt.acc[p], nt.evi[p], nt.drn[p], nt.que[p])
			}
		}
	}
}

// TestReplayDeterminism replays one script twice and compares snapshots.
func TestReplayDeterminism(t *testing.T) {
	script := func(g *gateway.Gateway) []string {
		g.AddTenant("A", 5, 50, 1)
		g.AddTenant("B", 20, 50, 0)
		log := []string{}
		run := func(now int64, n string, p int, s int64) {
			res, err := g.Ingest(now, n, p, s)
			if err != nil {
				log = append(log, fmt.Sprintf("reject:%s", errClass(err)))
				return
			}
			log = append(log, fmt.Sprintf("ok:%d:%d", res.Seq, len(res.Evicted)))
		}
		run(0, "A", 2, 40)
		run(0, "B", 1, 20)
		run(0, "B", 0, 20)
		run(5, "A", 2, 30)
		out, _ := g.Drain(6, 50)
		log = append(log, fmt.Sprintf("drain:%d", len(out)))
		return log
	}
	g1, _ := gateway.New(50)
	g2, _ := gateway.New(50)
	l1, l2 := script(g1), script(g2)
	if fmt.Sprint(l1) != fmt.Sprint(l2) {
		t.Fatalf("replay differs: %v vs %v", l1, l2)
	}
}
