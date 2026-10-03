package freqcap

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// naiveCtrl is a deliberately simple reference implementation written
// directly from the specification. It keeps the full admission history
// and recomputes every quantity by scanning, so it stays correct where
// the real controller uses incremental state (window eviction, streak
// counters, per-day maps).
type naiveCtrl struct {
	wg, cg, k, cc, g0 int64
	hist              map[string][]exposure
}

func newNaive(wg, cg, k, cc, g0 int64) *naiveCtrl {
	return &naiveCtrl{wg: wg, cg: cg, k: k, cc: cc, g0: g0, hist: map[string][]exposure{}}
}

// decideOn evaluates the three rules against an explicit history and
// returns the decision plus a human-readable justification.
func (n *naiveCtrl) decideOn(h []exposure, camp, cre string, now int64) (Decision, string) {
	if len(h) > 0 && now < h[len(h)-1].t {
		return Decision{false, RejectClockRollback},
			fmt.Sprintf("clock: now=%d < t_last=%d", now, h[len(h)-1].t)
	}
	if len(h) > 0 && h[len(h)-1].cre == cre {
		s := 0
		for i := len(h) - 1; i >= 0 && h[i].cre == cre; i-- {
			s++
		}
		sc := s
		if sc > 3 {
			sc = 3
		}
		need := n.g0 * int64(sc)
		if now-h[len(h)-1].t < need {
			return Decision{false, RejectCreativeInterval},
				fmt.Sprintf("interval: s=%d need=%d gap=%d", s, need, now-h[len(h)-1].t)
		}
	}
	var cg int64
	for _, e := range h {
		if e.t+n.wg > now {
			cg++
		}
	}
	if cg >= n.cg {
		return Decision{false, RejectGlobalWindow},
			fmt.Sprintf("window: cg=%d >= Cg=%d", cg, n.cg)
	}
	day := now / secondsPerDay
	nc := 0
	for _, e := range h {
		if e.t/secondsPerDay == day && e.camp == camp {
			nc++
		}
	}
	eff := n.cc - cg/n.k
	if eff < 1 {
		eff = 1
	}
	if int64(nc) >= eff {
		return Decision{false, RejectCampaignDaily},
			fmt.Sprintf("daily: n_c=%d >= E=%d (cg=%d K=%d)", nc, eff, cg, n.k)
	}
	return Decision{Allowed: true},
		fmt.Sprintf("admit: cg=%d E=%d n_c=%d", cg, eff, nc)
}

func validReq(camp, cre string, now int64) bool {
	return camp != "" && cre != "" && now >= 0 && now <= MaxNow
}

func (n *naiveCtrl) admit(user, camp, cre string, now int64) (Decision, string) {
	if user == "" || !validReq(camp, cre, now) {
		return Decision{false, RejectInvalidParam}, "invalid param"
	}
	d, why := n.decideOn(n.hist[user], camp, cre, now)
	if d.Allowed {
		n.hist[user] = append(n.hist[user], exposure{t: now, camp: camp, cre: cre})
	}
	return d, why
}

func (n *naiveCtrl) peek(user, camp, cre string, now int64) (Decision, string) {
	if user == "" || !validReq(camp, cre, now) {
		return Decision{false, RejectInvalidParam}, "invalid param"
	}
	return n.decideOn(n.hist[user], camp, cre, now)
}

func (n *naiveCtrl) batch(user string, reqs []Request) BatchResult {
	if user == "" || len(reqs) == 0 || len(reqs) > 1000 {
		return BatchResult{FailedIndex: -1, Reason: RejectInvalidParam}
	}
	work := append([]exposure(nil), n.hist[user]...)
	for i, r := range reqs {
		if !validReq(r.Camp, r.Cre, r.Now) {
			return BatchResult{FailedIndex: i, Reason: RejectInvalidParam}
		}
		d, _ := n.decideOn(work, r.Camp, r.Cre, r.Now)
		if !d.Allowed {
			return BatchResult{FailedIndex: i, Reason: d.Reason}
		}
		work = append(work, exposure{t: r.Now, camp: r.Camp, cre: r.Cre})
	}
	n.hist[user] = work
	return BatchResult{AdmittedAll: true, FailedIndex: -1}
}

// TestRandomAgainstNaive replays 2000 randomized operation sequences
// against both the real controller and the naive reference, requiring
// identical decisions and rejection reasons for every single operation.
// Every operation is logged with its input, output and justification.
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 2000
	users := []string{"u0", "u1", "u2"}
	camps := []string{"c0", "c1", "c2"}
	cres := []string{"x", "y", "z"}

	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*7919 + 13))
		wg := 1 + rng.Int63n(200)
		cg := 1 + rng.Int63n(8)
		k := 1 + rng.Int63n(4)
		cc := 1 + rng.Int63n(6)
		g0 := 1 + rng.Int63n(30)
		ctl, err := NewController(wg, cg, k, cc, g0)
		if err != nil {
			t.Fatalf("seq=%d: %v", seq, err)
		}
		ref := newNaive(wg, cg, k, cc, g0)
		t.Logf("seq=%d params Wg=%d Cg=%d K=%d Cc=%d g0=%d", seq, wg, cg, k, cc, g0)

		ops := 30 + rng.Intn(90)
		var clock int64
		for op := 0; op < ops; op++ {
			// Advance the clock: mostly forward, sometimes a rollback,
			// occasionally a jump across one or more day boundaries.
			switch r := rng.Intn(100); {
			case r < 5:
				clock -= rng.Int63n(120)
			case r < 10:
				clock += 86400 * (1 + rng.Int63n(3))
			default:
				clock += rng.Int63n(50)
			}
			user := users[rng.Intn(len(users))]
			camp := camps[rng.Intn(len(camps))]
			cre := cres[rng.Intn(len(cres))]
			kind := rng.Intn(100)

			switch {
			case kind < 55:
				got := ctl.Admit(user, camp, cre, clock)
				want, why := ref.admit(user, camp, cre, clock)
				t.Logf("seq=%d op=%d Admit(%s,%s,%s,%d) -> %s | %s",
					seq, op, user, camp, cre, clock, got, why)
				if got != want {
					t.Fatalf("seq=%d op=%d Admit(%s,%s,%s,%d): got %s, naive wants %s (%s)",
						seq, op, user, camp, cre, clock, got, want, why)
				}
			case kind < 80:
				got := ctl.Peek(user, camp, cre, clock)
				want, why := ref.peek(user, camp, cre, clock)
				t.Logf("seq=%d op=%d Peek(%s,%s,%s,%d) -> %s | %s",
					seq, op, user, camp, cre, clock, got, why)
				if got != want {
					t.Fatalf("seq=%d op=%d Peek(%s,%s,%s,%d): got %s, naive wants %s (%s)",
						seq, op, user, camp, cre, clock, got, want, why)
				}
			default:
				n := 1 + rng.Intn(6)
				reqs := make([]Request, n)
				for i := range reqs {
					reqs[i] = Request{
						Camp: camps[rng.Intn(len(camps))],
						Cre:  cres[rng.Intn(len(cres))],
						Now:  clock + rng.Int63n(40),
					}
				}
				got := ctl.AdmitBatch(user, reqs)
				want := ref.batch(user, reqs)
				t.Logf("seq=%d op=%d Batch(%s,%+v) -> %+v", seq, op, user, reqs, got)
				if got != want {
					t.Fatalf("seq=%d op=%d Batch(%s,%+v): got %+v, naive wants %+v",
						seq, op, user, reqs, got, want)
				}
			}
		}

		// Per-user bookkeeping invariants after the sequence.
		for _, u := range users {
			st := ctl.Stats(u)
			if st.Evicted > st.Admitted {
				t.Fatalf("seq=%d user=%s: evicted %d > admitted %d", seq, u, st.Evicted, st.Admitted)
			}
			if int64(st.Windowed) > cg {
				t.Fatalf("seq=%d user=%s: windowed %d > Cg %d", seq, u, st.Windowed, cg)
			}
			if int64(st.Windowed)+st.Evicted != st.Admitted {
				t.Fatalf("seq=%d user=%s: windowed+evicted != admitted (%d+%d != %d)",
					seq, u, st.Windowed, st.Evicted, st.Admitted)
			}
		}
	}
}

// TestEvictionBounds drives 1000 and then 100000 admissions through a
// short window and verifies that expired records are recycled as now
// advances: total pops never exceed total admissions and the retained
// window stays bounded regardless of the cumulative exposure count.
func TestEvictionBounds(t *testing.T) {
	// Wg=50, step 10: at most ~5 records are live at any time.
	c := mustNew(t, 50, 1_000_000, 1_000_000, 1_000_000, 1)
	var now int64
	for _, tier := range []int64{1_000, 100_000} {
		for c.Stats("u").Admitted < tier {
			d := c.Admit("u", "camp", "cre", now)
			if !d.Allowed {
				t.Fatalf("admit at now=%d rejected: %s", now, d.Reason)
			}
			now += 10
		}
		st := c.Stats("u")
		t.Logf("tier=%d: admitted=%d evicted=%d windowed=%d",
			tier, st.Admitted, st.Evicted, st.Windowed)
		if st.Evicted > st.Admitted {
			t.Fatalf("tier=%d: evicted %d > admitted %d", tier, st.Evicted, st.Admitted)
		}
		if int64(st.Windowed)+st.Evicted != st.Admitted {
			t.Fatalf("tier=%d: windowed+evicted != admitted", tier)
		}
		// The retained window must not grow with cumulative exposures:
		// only records with t+50 > now survive, i.e. at most 6.
		if st.Windowed > 6 {
			t.Fatalf("tier=%d: window retained %d records, want <= 6", tier, st.Windowed)
		}
	}
}

// TestConcurrentDisjointUsersDeterministic: concurrent operations on
// disjoint users must produce exactly the serial-per-user results.
func TestConcurrentDisjointUsersDeterministic(t *testing.T) {
	const users, ops = 8, 400
	mk := func() *Controller { return mustNew(t, 500, 5, 2, 4, 7) }
	ctl := mk()
	results := make([][]Decision, users)
	var wg sync.WaitGroup
	for u := 0; u < users; u++ {
		wg.Add(1)
		go func(u int) {
			defer wg.Done()
			user := fmt.Sprintf("u%d", u)
			rng := rand.New(rand.NewSource(int64(u + 1)))
			var now int64
			for i := 0; i < ops; i++ {
				now += rng.Int63n(20)
				camp := fmt.Sprintf("c%d", rng.Intn(3))
				cre := fmt.Sprintf("x%d", rng.Intn(3))
				results[u] = append(results[u], ctl.Admit(user, camp, cre, now))
			}
		}(u)
	}
	wg.Wait()
	// Serial replay on a fresh controller must match exactly.
	serial := mk()
	for u := 0; u < users; u++ {
		user := fmt.Sprintf("u%d", u)
		rng := rand.New(rand.NewSource(int64(u + 1)))
		var now int64
		for i := 0; i < ops; i++ {
			now += rng.Int63n(20)
			camp := fmt.Sprintf("c%d", rng.Intn(3))
			cre := fmt.Sprintf("x%d", rng.Intn(3))
			if got := serial.Admit(user, camp, cre, now); got != results[u][i] {
				t.Fatalf("user=%s op=%d: concurrent=%s serial=%s", user, i, results[u][i], got)
			}
		}
	}
}

// TestConcurrentSharedUsersInvariants hammers a few shared users with
// mixed Admit/Peek/AdmitBatch calls (run with -race) and then checks the
// safety invariants on every user's retained state.
func TestConcurrentSharedUsersInvariants(t *testing.T) {
	// Wg huge enough that nothing is evicted, so recs is the full history
	// and the interval invariant can be checked on adjacent records.
	ctl := mustNew(t, 1_000_000_000, 20, 3, 5, 4)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g*1000 + 7)))
			for i := 0; i < 500; i++ {
				user := fmt.Sprintf("shared%d", rng.Intn(4))
				camp := fmt.Sprintf("c%d", rng.Intn(3))
				cre := fmt.Sprintf("x%d", rng.Intn(3))
				now := rng.Int63n(2000)
				switch rng.Intn(3) {
				case 0:
					ctl.Admit(user, camp, cre, now)
				case 1:
					ctl.Peek(user, camp, cre, now)
				case 2:
					reqs := make([]Request, 1+rng.Intn(5))
					for j := range reqs {
						reqs[j] = Request{Camp: camp, Cre: cre, Now: now + int64(j)}
					}
					ctl.AdmitBatch(user, reqs)
				}
			}
		}(g)
	}
	wg.Wait()
	ctl.users.Range(func(key, val any) bool {
		user := key.(string)
		e := val.(*userEntry)
		e.mu.Lock()
		defer e.mu.Unlock()
		st := &e.st
		for i := 1; i < len(st.recs); i++ {
			if st.recs[i].t < st.recs[i-1].t {
				t.Errorf("user=%s: records not ordered by time", user)
			}
			// Adjacent admitted exposures with the same creative must be
			// at least g0 apart.
			if st.recs[i].cre == st.recs[i-1].cre &&
				st.recs[i].t-st.recs[i-1].t < ctl.g0 {
				t.Errorf("user=%s: interval violated at %v -> %v",
					user, st.recs[i-1], st.recs[i])
			}
		}
		if int64(len(st.recs)) > ctl.cg {
			t.Errorf("user=%s: window %d exceeds Cg %d", user, len(st.recs), ctl.cg)
		}
		for _, r := range st.recs {
			if r.t+ctl.wg <= st.lastT {
				t.Errorf("user=%s: expired record %+v retained", user, r)
			}
		}
		for camp, n := range st.counts {
			if n > int(ctl.cc) {
				t.Errorf("user=%s camp=%s: daily count %d exceeds Cc %d", user, camp, n, ctl.cc)
			}
		}
		if st.evicted > st.admitted {
			t.Errorf("user=%s: evicted %d > admitted %d", user, st.evicted, st.admitted)
		}
		return true
	})
}
