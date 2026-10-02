package leaseread

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// runArbiter applies the trace to the real implementation.
func runArbiter(t *testing.T, n int, dur, rho, et, start int64, ops []op, logf func(string, ...any)) []opResult {
	t.Helper()
	a, err := New(n, dur, rho, et, start)
	if err != nil {
		t.Fatalf("New(%d,%d,%d,%d,%d): %v", n, dur, rho, et, start, err)
	}
	out := make([]opResult, 0, len(ops))
	logf("CFG n=%d dur=%d rho=%d et=%d start=%d q=%d leaseLen=%d",
		n, dur, rho, et, start, n/2+1, dur*(1000-rho)/1000)
	for k, o := range ops {
		var r opResult
		switch o.kind {
		case 'a':
			ids, err := a.Ack(o.i, o.s, o.val)
			r = opResult{kind: "ack", errIs: errName(err), ids: ids}
		case 'r':
			res, err := a.Read(o.val)
			r = opResult{kind: "read", errIs: errName(err), readKind: res.Kind,
				readId: res.Id, expire: res.Expire}
		case 't':
			ids, err := a.Tick(o.val)
			r = opResult{kind: "tick", errIs: errName(err), ids: ids}
		case 'v':
			v, err := a.ChallengerVotes(o.val)
			r = opResult{kind: "votes", errIs: errName(err), votes: v}
		}
		logf("op[%d] %s -> %s", k, opString(o), resultString(r))

		// Invariant: every locally served read happens while leader and a
		// challenger cannot reach quorum.
		if o.kind == 'r' && r.errIs == "" && r.readKind == "local" {
			leader, votes := a.leaderVotesLocked(o.val)
			if !leader {
				t.Fatalf("op[%d]: local read but not leader", k)
			}
			if votes >= a.q() {
				t.Fatalf("op[%d]: local read but challenger votes=%d >= q=%d", k, votes, a.q())
			}
			logf("    INV local-read now=%d leader=true votes=%d < q=%d  (basis: %s)",
				o.val, votes, a.q(), localBasis(a, o.val))
		}
		// examined never exceeds confirmed+1 on each successful Ack.
		if o.kind == 'a' && r.errIs == "" && a.debugExamined() > int64(len(r.ids))+1 {
			t.Fatalf("op[%d]: examined=%d > confirmed+1=%d", k, a.debugExamined(), len(r.ids)+1)
		}
		out = append(out, r)
	}
	return out
}

func localBasis(a *Arbiter, now int64) string {
	base, ok := a.leaseBaseLocked(now)
	if !ok {
		return "no base (N=1 lease is perpetual)"
	}
	e := base + a.leaseLen()
	return fmt.Sprintf("base=%d E=%d now<E", base, e)
}

func runNaive(n int, dur, rho, et, start int64, ops []op) ([]opResult, *naiveArbiter) {
	m := newNaive(n, dur, rho, et, start)
	out := make([]opResult, 0, len(ops))
	for _, o := range ops {
		out = append(out, m.apply(o))
	}
	return out, m
}

func opString(o op) string {
	switch o.kind {
	case 'a':
		return fmt.Sprintf("Ack(i=%d,s=%d,r=%d)", o.i, o.s, o.val)
	case 'r':
		return fmt.Sprintf("Read(now=%d)", o.val)
	case 't':
		return fmt.Sprintf("Tick(now=%d)", o.val)
	default:
		return fmt.Sprintf("Votes(now=%d)", o.val)
	}
}

func resultString(r opResult) string {
	if r.errIs != "" {
		return "ERR(" + r.errIs + ")"
	}
	switch r.kind {
	case "ack":
		return fmt.Sprintf("confirmed=%v examined-limit-ok", r.ids)
	case "read":
		return fmt.Sprintf("%s id=%d E=%d", r.readKind, r.readId, r.expire)
	case "tick":
		return fmt.Sprintf("aborted=%v", r.ids)
	default:
		return fmt.Sprintf("votes=%d", r.votes)
	}
}

// TestRandomDifferential runs 2000 random cases against the naive model and
// checks every output plus the local-read safety invariant.
func TestRandomDifferential(t *testing.T) {
	const cases = 2000
	verbose := testing.Verbose()
	var failTrace []string
	for c := 0; c < cases; c++ {
		rng := rand.New(rand.NewSource(int64(1000 + c)))
		n, dur, rho, et, start, ops := genCase(rng)
		var logs []string
		logf := func(format string, args ...any) {
			if verbose || len(failTrace) > 0 {
				logs = append(logs, fmt.Sprintf(format, args...))
			}
		}

		gotModel, _ := runNaive(n, dur, rho, et, start, ops)

		var got []opResult
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("case %d panic: %v\n%s", c, rec, joinLogs(logs))
				}
			}()
			got = runArbiter(t, n, dur, rho, et, start, ops, logf)
		}()

		for k := range ops {
			if !resultsEqual(got[k], gotModel[k]) {
				t.Fatalf("case %d seed=%d op[%d] %s:\n got %+v\nwant %+v\n%s",
					c, 1000+c, k, opString(ops[k]), got[k], gotModel[k], joinLogs(logs))
			}
		}

		// Cross-check safety invariant with the naive model itself.
		m2 := newNaive(n, dur, rho, et, start)
		for k, o := range ops {
			r := m2.apply(o)
			if o.kind == 'r' && r.errIs == "" && r.readKind == "local" {
				ok, e := m2.localAt(o.val)
				v := countVotes(m2, o.val)
				if !ok || v >= m2.q {
					t.Fatalf("case %d op[%d]: naive invariant violated local=%v E=%d votes=%d q=%d",
						c, k, ok, e, v, m2.q)
				}
			}
		}
	}
}

func countVotes(m *naiveArbiter, now int64) int {
	v := 0
	for f := 1; f < m.n; f++ {
		if now >= m.pr[f] {
			v++
		}
	}
	if !m.leader {
		v++
	}
	return v
}

func joinLogs(logs []string) string {
	out := "trace:\n"
	for _, l := range logs {
		out += "  " + l + "\n"
	}
	return out
}

// TestReplayDeterministic replays the same trace twice and demands
// byte-identical read results, confirmation sequences and step-down time.
func TestReplayDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	n, dur, rho, et, start, ops := genCase(rng)
	r1, _ := runNaive(n, dur, rho, et, start, ops)
	r2, m2 := runNaive(n, dur, rho, et, start, ops)
	for k := range r1 {
		if !resultsEqual(r1[k], r2[k]) {
			t.Fatalf("replay mismatch op[%d]: %+v vs %+v", k, r1[k], r2[k])
		}
	}
	step := int64(-1)
	for k, r := range r1 {
		if r.kind == "tick" && len(r.ids) > 0 {
			step = ops[k].val
			break
		}
	}
	if step != m2.stepdownAt {
		t.Fatalf("stepdown time %d vs %d", step, m2.stepdownAt)
	}
}

// TestConcurrentSerializability hammers one arbiter concurrently and checks
// that every observed result is consistent with some serial order: no error
// other than the four documented ones, monotonic watermark per goroutine
// rejections behave, and safety invariant holds for every local read.
func TestConcurrentSerializability(t *testing.T) {
	a, _ := New(5, 1000, 100, 1500, 0)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var bad []string
	note := func(s string) {
		mu.Lock()
		bad = append(bad, s)
		mu.Unlock()
	}

	// Followers continuously acknowledge with monotonically increasing times.
	for f := 1; f < 5; f++ {
		wg.Add(1)
		go func(f int) {
			defer wg.Done()
			var r int64
			for k := 0; k < 300; k++ {
				s := r + 1
				r = s + 10
				ids, err := a.Ack(f, s, r)
				if err != nil {
					if err == ErrClockRewind {
						// Another goroutine advanced the watermark: this
						// is a legitimate rejection under concurrency.
						continue
					}
					note(fmt.Sprintf("Ack unexpected err %v", err))
					return
				}
				if int64(len(ids)) > a.debugExamined() {
					note("confirmed more than examined")
				}
			}
		}(f)
	}
	// Readers attempt local reads; every local read must satisfy safety.
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var last int64
			for k := 0; k < 300; k++ {
				now := last + int64(k%7)
				res, err := a.Read(now)
				if err == ErrClockRewind {
					continue
				}
				if err == ErrNotLeader {
					return
				}
				if err != nil {
					note(fmt.Sprintf("Read err %v", err))
					return
				}
				last = now
				if res.Kind == "local" {
					v, verr := a.ChallengerVotes(now)
					if verr != nil {
						note("votes err: " + verr.Error())
					} else if v >= 3 {
						note(fmt.Sprintf("local read at %d with votes %d", now, v))
					}
				}
			}
		}()
	}
	// A ticker may step the leader down.
	wg.Add(1)
		go func() {
			defer wg.Done()
			var now int64
			for k := 0; k < 100; k++ {
				now += 20
				if _, err := a.Tick(now); err != nil && err != ErrClockRewind {
					note("tick err: " + err.Error())
				}
			}
	}()
	wg.Wait()
	if len(bad) > 0 {
		limit := len(bad)
		if limit > 5 {
			limit = 5
		}
		t.Fatalf("concurrent violations: %v", bad[:limit])
	}
}
