package gateway_test

import (
	"math/rand"
	"testing"

	"ontology/gateway"
	"ontology/queue"
)

type op struct {
	drain        bool
	now          int64
	tenant       string
	prio         int
	size, budget int64
}

func genOps(rng *rand.Rand, names []string, n int, capB int64) []op {
	var ops []op
	var now int64
	for i := 0; i < n; i++ {
		now += int64(rng.Intn(20))
		if rng.Intn(20) == 0 {
			now -= int64(rng.Intn(10)) // 偶发时钟回退
		}
		if now < 0 {
			now = 0
		}
		if rng.Intn(3) == 0 {
			ops = append(ops, op{drain: true, now: now, budget: int64(rng.Intn(int(2 * capB)))})
		} else {
			ops = append(ops, op{
				now:    now,
				tenant: names[rng.Intn(len(names))],
				prio:   rng.Intn(4) - 1, // 偶发非法优先级
				size:   int64(1 + rng.Intn(int(capB))),
			})
		}
	}
	return ops
}

func sameRecs(a []queue.Record, b []simRec) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Seq != b[i].seq || a[i].Tenant != b[i].tenant ||
			a[i].Prio != b[i].prio || a[i].Size != b[i].size {
			return false
		}
	}
	return true
}

func TestRandomAgainstNaiveSim(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	const capB = 200
	names := []string{"a", "b", "c", "d", "e"}
	g, _ := gateway.New(capB)
	s := newSim(capB)
	for _, n := range names {
		rate := int64(rng.Intn(50))
		burst := int64(50 + rng.Intn(150))
		mp := rng.Intn(3)
		if err := g.AddTenant(n, rate, burst, mp); err != nil {
			t.Fatal(err)
		}
		s.tenants[n] = &simTenant{rate: rate, burst: burst, maxPrio: mp, tokens: burst * 1000}
		t.Logf("AddTenant(%s, rate=%d, burst=%d, maxPrio=%d)", n, rate, burst, mp)
	}
	for i, o := range genOps(rng, names, 3000, capB) {
		if o.drain {
			got, gErr := g.Drain(o.now, o.budget)
			want, sErr := s.drain(o.now, o.budget)
			t.Logf("Drain(%d,%d) -> %d 条 err=%v", o.now, o.budget, len(got), gErr)
			if gErr != sErr || !sameRecs(got, want) {
				t.Fatalf("step %d Drain: got %v/%v want %v/%v", i, got, gErr, want, sErr)
			}
			continue
		}
		gr, gErr := g.Ingest(o.now, o.tenant, o.prio, o.size)
		sev, sErr := s.ingest(o.now, o.tenant, o.prio, o.size)
		t.Logf("Ingest(%d,%s,%d,%d) -> seq=%d evicted=%v err=%v",
			o.now, o.tenant, o.prio, o.size, gr.Seq, gr.Evicted, gErr)
		if gErr != sErr || !sameRecs(gr.Evicted, sev) {
			t.Fatalf("step %d Ingest: got %v/%v want %v/%v", i, gr.Evicted, gErr, sev, sErr)
		}
	}
	stats := g.Stats()
	for name, tn := range s.tenants {
		tok, lr, _ := g.TenantState(name)
		if tok != tn.tokens || lr != tn.lastRefill {
			t.Fatalf("%s state=(%d,%d) want (%d,%d)", name, tok, lr, tn.tokens, tn.lastRefill)
		}
		var q [3]int64
		for _, r := range s.q {
			if r.tenant == name {
				q[r.prio] += r.size
			}
		}
		st := stats[name]
		if st.QueuedBytes != q {
			t.Fatalf("%s queued=%v want %v", name, st.QueuedBytes, q)
		}
		for p := 0; p < 3; p++ {
			if st.AcceptedBytes[p] != st.EvictedBytes[p]+st.DrainedBytes[p]+q[p] {
				t.Fatalf("%s prio %d stats=%+v 不变量不成立", name, p, st)
			}
		}
	}
	if g.Used() != s.used() {
		t.Fatalf("used=%d want %d", g.Used(), s.used())
	}
}

func TestReplayDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	const capB = 100
	names := []string{"x", "y", "z"}
	ops := genOps(rng, names, 1500, capB)
	run := func() []string {
		g, _ := gateway.New(capB)
		for _, n := range names {
			if err := g.AddTenant(n, 5, 80, 0); err != nil {
				t.Fatal(err)
			}
		}
		var log []string
		for _, o := range ops {
			if o.drain {
				recs, err := g.Drain(o.now, o.budget)
				log = append(log, sprintRecs("D", recs, err))
			} else {
				r, err := g.Ingest(o.now, o.tenant, o.prio, o.size)
				log = append(log, sprintRecs("I", r.Evicted, err))
			}
		}
		return log
	}
	a, b := run(), run()
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("step %d 重放不一致: %q vs %q", i, a[i], b[i])
		}
	}
}

func sprintRecs(tag string, recs []queue.Record, err error) string {
	s := tag + ":"
	for _, r := range recs {
		s += " " + r.Tenant + "/" + string(rune('0'+r.Prio)) + "/" +
			string(rune('0'+r.Size%10)) + "#" + string(rune('0'+r.Seq%10))
	}
	if err != nil {
		s += " !" + err.Error()
	}
	return s
}
