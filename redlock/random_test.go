package redlock

import (
	"errors"
	"math/rand"
	"reflect"
	"testing"
)

// engineSnapshot captures the full observable state of an Engine.
type engineSnapshot struct {
	clock   int64
	token   int64
	quiet   []int64
	records []map[string]record
}

func snapshotEngine(e *Engine) engineSnapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := engineSnapshot{
		clock:   e.clock,
		token:   e.nextToken,
		quiet:   make([]int64, len(e.nodes)),
		records: make([]map[string]record, len(e.nodes)),
	}
	for i := range e.nodes {
		s.quiet[i] = e.nodes[i].quiet
		m := make(map[string]record, len(e.nodes[i].records))
		for res, rec := range e.nodes[i].records {
			m[res] = rec
		}
		s.records[i] = m
	}
	return s
}

func snapshotNaive(nv *naiveEngine) engineSnapshot {
	s := engineSnapshot{
		clock:   nv.clock,
		token:   nv.nextToken,
		quiet:   append([]int64(nil), nv.quiet...),
		records: make([]map[string]record, nv.n),
	}
	for i := range nv.recs {
		m := make(map[string]record, len(nv.recs[i]))
		for res, rec := range nv.recs[i] {
			m[res] = record{token: rec.token, expiry: rec.expiry}
		}
		s.records[i] = m
	}
	return s
}

// opRecord stores one operation and its expected outcome so the whole
// sequence can be replayed on a fresh engine to verify determinism.
type opRecord struct {
	desc  string
	run   func(e *Engine) (AcquireResult, int, error)
	wantR AcquireResult
	wantN int
	wantE error
}

func sameErr(got, want error) bool {
	return errClass(got) == errClass(want)
}

func errClass(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, ErrInvalidConfig):
		return 1
	case errors.Is(err, ErrInvalidArgument):
		return 2
	case errors.Is(err, ErrInvalidTime):
		return 3
	case errors.Is(err, ErrClockRewind):
		return 4
	default:
		return -1
	}
}

// successInfo tracks the last successful acquire per resource for the
// disjoint-validity-interval assertion.
type successInfo struct {
	cend      int64
	until     int64
	token     int64
	unlockNow int64 // now of the first Unlock that removed this token, -1 if none
}

// TestRandomAgainstNaive replays 2000 random configurations and
// operation sequences against the naive reference implementation,
// asserts identical tokens, grant counts and node records, verifies
// that replaying the same sequence on a fresh engine reproduces the
// exact same outputs, and checks that validity intervals of successful
// acquires on the same resource never overlap.
func TestRandomAgainstNaive(t *testing.T) {
	const iterations = 2000
	resources := []string{"alpha", "beta", "gamma"}

	for iter := 0; iter < iterations; iter++ {
		rng := rand.New(rand.NewSource(int64(iter)))
		n := 1 + rng.Intn(9)
		tn := int64(1 + rng.Intn(200))
		d := int64(rng.Intn(1001))
		maxTTL := int64(1 + rng.Intn(2000))

		eng, err := New(n, tn, d, maxTTL)
		if err != nil {
			t.Fatalf("iter %d: New: %v", iter, err)
		}
		nv := newNaive(n, tn, d, maxTTL)
		t.Logf("iter %d: N=%d Tn=%d D=%d MaxTTL=%d", iter, n, tn, d, maxTTL)

		type issuedToken struct {
			res     string
			token   int64
			success bool
		}
		var issued []issuedToken
		lastSuccess := map[string]*successInfo{}
		var replay []opRecord

		ops := 5 + rng.Intn(45)
		for op := 0; op < ops; op++ {
			clock := eng.Clock()
			switch rng.Intn(10) {
			case 0, 1, 2, 3, 4: // Acquire
				res := resources[rng.Intn(len(resources))]
				if rng.Intn(20) == 0 {
					res = ""
				}
				ttl := int64(1 + rng.Int63n(maxTTL))
				// Half of the acquires use success-friendly parameters
				// (max ttl, short rtts) so quorum+validity successes and
				// consecutive same-resource successes are exercised often.
				friendly := rng.Intn(2) == 0
				if friendly {
					ttl = maxTTL
				} else {
					switch rng.Intn(20) {
					case 0:
						ttl = 0
					case 1:
						ttl = maxTTL + 1
					}
				}
				start := clock + rng.Int63n(50)
				switch rng.Intn(20) {
				case 0:
					start = clock - 1
				case 1:
					start = maxTime + 1
				case 2:
					start = -1
				}
				rtt := make([]int64, n)
				for i := range rtt {
					if friendly {
						if rng.Intn(10) == 0 {
							rtt[i] = -1
						} else {
							rtt[i] = rng.Int63n(tn/4 + 1)
						}
					} else {
						switch rng.Intn(20) {
						case 0, 1, 2:
							rtt[i] = -1
						case 3:
							rtt[i] = -2
						case 4:
							rtt[i] = 2*tn + 1
						default:
							rtt[i] = rng.Int63n(2*tn + 1)
						}
					}
				}
				if rng.Intn(20) == 0 && len(rtt) > 0 {
					rtt = rtt[:len(rtt)-1]
				}

				gotR, gotE := eng.Acquire(res, ttl, start, rtt)
				wantR, wantE := nv.acquire(res, ttl, start, rtt)
				t.Logf("iter %d op %d: Acquire(%q, ttl=%d, start=%d, rtt=%v) -> %+v err=%v",
					iter, op, res, ttl, start, rtt, gotR, gotE)
				if !sameErr(gotE, wantE) || gotR != wantR {
					t.Fatalf("iter %d op %d: Acquire(%q,%d,%d,%v): got (%+v, %v), naive (%+v, %v)",
						iter, op, res, ttl, start, rtt, gotR, gotE, wantR, wantE)
				}
				rttCopy := append([]int64(nil), rtt...)
				replay = append(replay, opRecord{
					desc: "Acquire",
					run: func(e *Engine) (AcquireResult, int, error) {
						r, err := e.Acquire(res, ttl, start, rttCopy)
						return r, 0, err
					},
					wantR: gotR, wantE: gotE,
				})
				if gotE != nil {
					break
				}
				issued = append(issued, issuedToken{res: res, token: gotR.Token, success: gotR.Success})
				if gotR.Success {
					if ls, ok := lastSuccess[res]; ok {
						bound := ls.until
						if ls.unlockNow >= 0 && ls.unlockNow < bound {
							bound = ls.unlockNow
						}
						t.Logf("iter %d op %d: disjoint check res=%q prevUntil=%d unlockNow=%d bound=%d newCend=%d",
							iter, op, res, ls.until, ls.unlockNow, bound, gotR.Cend)
						if gotR.Cend < bound {
							t.Fatalf("iter %d op %d: validity intervals overlap on %q: prev until=%d unlockNow=%d, new cend=%d",
								iter, op, res, ls.until, ls.unlockNow, gotR.Cend)
						}
					}
					lastSuccess[res] = &successInfo{cend: gotR.Cend, until: gotR.Until, token: gotR.Token, unlockNow: -1}
				}

			case 5, 6: // Unlock
				res := resources[rng.Intn(len(resources))]
				if rng.Intn(20) == 0 {
					res = ""
				}
				var k int64
				if len(issued) > 0 && rng.Intn(10) < 7 {
					k = issued[rng.Intn(len(issued))].token
				} else {
					k = int64(rng.Intn(4))
				}
				now := clock + rng.Int63n(50)
				switch rng.Intn(20) {
				case 0:
					now = clock - 1
				case 1:
					now = maxTime + 1
				}

				gotN, gotE := eng.Unlock(res, k, now)
				wantN, wantE := nv.unlock(res, k, now)
				t.Logf("iter %d op %d: Unlock(%q, k=%d, now=%d) -> removed=%d err=%v",
					iter, op, res, k, now, gotN, gotE)
				if !sameErr(gotE, wantE) || gotN != wantN {
					t.Fatalf("iter %d op %d: Unlock(%q,%d,%d): got (%d, %v), naive (%d, %v)",
						iter, op, res, k, now, gotN, gotE, wantN, wantE)
				}
				replay = append(replay, opRecord{
					desc: "Unlock",
					run: func(e *Engine) (AcquireResult, int, error) {
						c, err := e.Unlock(res, k, now)
						return AcquireResult{}, c, err
					},
					wantN: gotN, wantE: gotE,
				})
				if gotE == nil && gotN > 0 {
					if ls, ok := lastSuccess[res]; ok && ls.token == k && (ls.unlockNow < 0 || now < ls.unlockNow) {
						ls.unlockNow = now
					}
				}

			case 7: // Restart
				i := rng.Intn(n)
				switch rng.Intn(20) {
				case 0:
					i = -1
				case 1:
					i = n
				}
				now := clock + rng.Int63n(50)
				switch rng.Intn(20) {
				case 0:
					now = clock - 1
				case 1:
					now = maxTime + 1
				}

				gotE := eng.Restart(i, now)
				wantE := nv.restart(i, now)
				t.Logf("iter %d op %d: Restart(i=%d, now=%d) -> err=%v", iter, op, i, now, gotE)
				if !sameErr(gotE, wantE) {
					t.Fatalf("iter %d op %d: Restart(%d,%d): got %v, naive %v", iter, op, i, now, gotE, wantE)
				}
				replay = append(replay, opRecord{
					desc:  "Restart",
					run:   func(e *Engine) (AcquireResult, int, error) { return AcquireResult{}, 0, e.Restart(i, now) },
					wantE: gotE,
				})

			default: // Count
				res := resources[rng.Intn(len(resources))]
				if rng.Intn(20) == 0 {
					res = ""
				}
				var k int64
				if len(issued) > 0 && rng.Intn(10) < 7 {
					k = issued[rng.Intn(len(issued))].token
				} else {
					k = int64(rng.Intn(4))
				}
				now := clock + rng.Int63n(50)
				switch rng.Intn(20) {
				case 0:
					now = clock - 1
				case 1:
					now = maxTime + 1
				}

				gotN, gotE := eng.Count(res, k, now)
				wantN, wantE := nv.count(res, k, now)
				t.Logf("iter %d op %d: Count(%q, k=%d, now=%d) -> %d err=%v",
					iter, op, res, k, now, gotN, gotE)
				if !sameErr(gotE, wantE) || gotN != wantN {
					t.Fatalf("iter %d op %d: Count(%q,%d,%d): got (%d, %v), naive (%d, %v)",
						iter, op, res, k, now, gotN, gotE, wantN, wantE)
				}
				replay = append(replay, opRecord{
					desc: "Count",
					run: func(e *Engine) (AcquireResult, int, error) {
						c, err := e.Count(res, k, now)
						return AcquireResult{}, c, err
					},
					wantN: gotN, wantE: gotE,
				})
			}

			// Cross-check full state after every operation.
			gotSnap := snapshotEngine(eng)
			wantSnap := snapshotNaive(nv)
			if !reflect.DeepEqual(gotSnap, wantSnap) {
				t.Fatalf("iter %d op %d: state divergence\nengine: %+v\nnaive:  %+v", iter, op, gotSnap, wantSnap)
			}
		}

		// Replay the identical sequence on a fresh engine: tokens, grant
		// counts, outputs and final records must be exactly reproduced.
		fresh, err := New(n, tn, d, maxTTL)
		if err != nil {
			t.Fatalf("iter %d: New for replay: %v", iter, err)
		}
		for op, rec := range replay {
			gotR, gotN, gotE := rec.run(fresh)
			if gotR != rec.wantR || gotN != rec.wantN || !sameErr(gotE, rec.wantE) {
				t.Fatalf("iter %d replay op %d (%s): got (%+v, %d, %v), want (%+v, %d, %v)",
					iter, op, rec.desc, gotR, gotN, gotE, rec.wantR, rec.wantN, rec.wantE)
			}
		}
		if got, want := snapshotEngine(fresh), snapshotEngine(eng); !reflect.DeepEqual(got, want) {
			t.Fatalf("iter %d: replay final state divergence\nreplay: %+v\norig:   %+v", iter, got, want)
		}
		t.Logf("iter %d: %d ops, final T=%d, tokens issued=%d, replay identical",
			iter, len(replay), eng.Clock(), len(issued))
	}
}
