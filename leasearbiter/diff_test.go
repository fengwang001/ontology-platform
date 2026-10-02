package leasearbiter

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// op is one recorded operation so runs are fully replayable and loggable.
type op struct {
	kind string
	i    int
	s, r int64
	now  int64
}

type opResult struct {
	err      string
	local    bool
	pending  bool
	aborted  bool
	id       int
	expiry   int64
	list     []int
	votes    int
	examined int
}

func opString(o op) string {
	switch o.kind {
	case "Ack":
		return fmt.Sprintf("Ack(i=%d,s=%d,r=%d)", o.i, o.s, o.r)
	case "Read":
		return fmt.Sprintf("Read(now=%d)", o.now)
	case "Tick":
		return fmt.Sprintf("Tick(now=%d)", o.now)
	default:
		return fmt.Sprintf("ChallengerVotes(now=%d)", o.now)
	}
}

func resultString(r opResult) string {
	if r.err != "" {
		return "err=" + r.err
	}
	var b strings.Builder
	switch {
	case r.local:
		fmt.Fprintf(&b, "local(E=%d)", r.expiry)
	case r.pending:
		fmt.Fprintf(&b, "pending(id=%d)", r.id)
	case r.aborted:
		fmt.Fprintf(&b, "aborted=%v", r.list)
	case r.list != nil:
		fmt.Fprintf(&b, "ids=%v", r.list)
	default:
		fmt.Fprintf(&b, "votes=%d", r.votes)
	}
	if r.examined != 0 {
		fmt.Fprintf(&b, " examined=%d", r.examined)
	}
	return b.String()
}

func runOnArbiter(a *Arbiter, o op) opResult {
	switch o.kind {
	case "Ack":
		list, err := a.Ack(o.i, o.s, o.r)
		return opResult{err: errName(err), list: append([]int(nil), list...), examined: a.Examined()}
	case "Read":
		r, err := a.Read(o.now)
		return opResult{err: errName(err), local: r.Local, pending: r.Pending, id: r.ReadID, expiry: r.LeaseExpiry}
	case "Tick":
		list, err := a.Tick(o.now)
		return opResult{err: errName(err), aborted: true, list: append([]int(nil), list...)}
	default:
		v, err := a.ChallengerVotes(o.now)
		return opResult{err: errName(err), votes: v}
	}
}

func runOnNaive(m *naiveArbiter, o op) opResult {
	var (
		out naiveOutcome
		err error
	)
	switch o.kind {
	case "Ack":
		out, err = m.ack(o.i, o.s, o.r)
	case "Read":
		out, err = m.read(o.now)
	case "Tick":
		out, err = m.tick(o.now)
	default:
		out, err = m.challenger(o.now)
	}
	return opResult{
		err: errName(err), local: out.local, pending: out.pending,
		aborted: o.kind == "Tick", id: out.id, expiry: out.expiry,
		list:  append(append([]int(nil), out.confirmed...), out.aborted...),
		votes: out.votes, examined: out.examined,
	}
}

func resultsEqual(x, y opResult) bool {
	if x.err != y.err || x.local != y.local || x.pending != y.pending ||
		x.aborted != y.aborted || x.id != y.id || x.expiry != y.expiry || x.votes != y.votes ||
		x.examined != y.examined || len(x.list) != len(y.list) {
		return false
	}
	for i := range x.list {
		if x.list[i] != y.list[i] {
			return false
		}
	}
	return true
}

type testConfig struct {
	n     int
	dur   int64
	rho   int
	et    int64
	start int64
	ops   []op
}

func randomConfig(rng *rand.Rand) testConfig {
	n := 1 + rng.Intn(9)
	dur := int64(1 + rng.Intn(100))
	rho := rng.Intn(1000)
	et := dur + int64(rng.Intn(300))
	start := int64(rng.Intn(50))
	c := testConfig{n: n, dur: dur, rho: rho, et: et, start: start}

	now := start
	for k := 0; k < 60; k++ {
		now += int64(rng.Intn(3))
		switch rng.Intn(10) {
		case 0, 1, 2: // Ack
			i := 1
			if n > 1 {
				i = 1 + rng.Intn(n-1)
			}
			s := int64(0)
			if now > 0 {
				s = int64(rng.Intn(int(now) + 1))
			}
			if s > now {
				s = now
			}
			c.ops = append(c.ops, op{kind: "Ack", i: i, s: s, r: now})
		case 3, 4, 5, 6: // Read
			c.ops = append(c.ops, op{kind: "Read", now: now})
		case 7, 8: // Tick
			c.ops = append(c.ops, op{kind: "Tick", now: now})
		default: // ChallengerVotes
			c.ops = append(c.ops, op{kind: "Challenger", now: now})
		}
	}
	return c
}

// TestNaiveDifferential runs 2000 random configurations/sequences against
// both the production Arbiter and the naive oracle, asserting identical
// results, the lease safety property, examined bounds and exact replay
// determinism. Inputs, outputs and the deciding rule are logged.
func TestNaiveDifferential(t *testing.T) {
	const cases = 2000
	totalLocalReads := 0
	for seed := int64(0); seed < cases; seed++ {
		rng := rand.New(rand.NewSource(seed))
		cfg := randomConfig(rng)

		var log strings.Builder
		fmt.Fprintf(&log, "seed=%d config: N=%d Dur=%d Rho=%d Et=%d start=%d q=%d\n",
			seed, cfg.n, cfg.dur, cfg.rho, cfg.et, cfg.start, cfg.n/2+1)

		run := func() []opResult {
			a, err := New(cfg.n, cfg.dur, cfg.rho, cfg.et, cfg.start)
			if err != nil {
				t.Fatalf("seed=%d unexpected config error: %v", seed, err)
			}
			results := make([]opResult, 0, len(cfg.ops))
			for step, o := range cfg.ops {
				r := runOnArbiter(a, o)
				fmt.Fprintf(&log, "  step %2d: %-28s => %s\n", step, opString(o), resultString(r))
				results = append(results, r)
			}
			return results
		}

		m, err := naiveNew(cfg.n, cfg.dur, cfg.rho, cfg.et, cfg.start)
		if err != nil {
			t.Fatalf("seed=%d naive config: %v", seed, err)
		}

		realResults := run()
		// Replay the identical sequence: results must reproduce exactly.
		replayResults := run()
		for i := range realResults {
			if !resultsEqual(realResults[i], replayResults[i]) {
				t.Fatalf("seed=%d replay mismatch at step %d:\nfirst=%s\nreplay=%s\n%s",
					seed, i, resultString(realResults[i]), resultString(replayResults[i]), log.String())
			}
		}

		for step, o := range cfg.ops {
			rm := runOnNaive(m, o)
			ra := realResults[step]
			if !resultsEqual(ra, rm) {
				t.Fatalf("seed=%d step=%d %s differential mismatch:\nreal =%s\nnaive=%s\n%s",
					seed, step, opString(o), resultString(ra), resultString(rm), log.String())
			}
			if o.kind == "Ack" && ra.err == "" {
				if ra.examined > len(ra.list)+1 {
					t.Fatalf("seed=%d examined %d > confirmed+1 %d", seed, ra.examined, len(ra.list)+1)
				}
			}
		}

		// Safety assertion replayed directly on a fresh arbiter: for every
		// local read at now, leader is still true and ChallengerVotes(now)
		// is below quorum.
		a, _ := New(cfg.n, cfg.dur, cfg.rho, cfg.et, cfg.start)
		for _, o := range cfg.ops {
			r := runOnArbiter(a, o)
			if o.kind == "Read" && r.local {
				totalLocalReads++
				votes, verr := a.ChallengerVotes(o.now)
				if verr != nil {
					t.Fatalf("seed=%d ChallengerVotes at local read: %v\n%s", seed, verr, log.String())
				}
				if votes >= cfg.n/2+1 {
					t.Fatalf("seed=%d UNSAFE local read %s: challengerVotes=%d >= q=%d\n%s",
						seed, opString(o), votes, cfg.n/2+1, log.String())
				}
			}
		}
		t.Logf("seed=%-4d N=%d Dur=%3d Rho=%3d Et=%3d start=%2d ops=%d localReadsSoFar=%d",
			seed, cfg.n, cfg.dur, cfg.rho, cfg.et, cfg.start, len(cfg.ops), totalLocalReads)
	}
	t.Logf("differential run complete: %d cases, %d local reads all verified under-quorum",
		cases, totalLocalReads)
}

// TestInvalidConfigDifferential covers rejected configurations too.
func TestInvalidConfigDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for k := 0; k < 500; k++ {
		n := -2 + rng.Intn(14)
		dur := int64(-5 + rng.Intn(1_000_000_010))
		rho := -1 + rng.Intn(1002)
		et := int64(-5 + rng.Intn(1_000_000_010))
		start := int64(-5 + rng.Intn(100))
		_, e1 := New(n, dur, rho, et, start)
		_, e2 := naiveNew(n, dur, rho, et, start)
		if errName(e1) != errName(e2) {
			t.Fatalf("config (%d,%d,%d,%d,%d): real=%v naive=%v", n, dur, rho, et, start, e1, e2)
		}
	}
}
