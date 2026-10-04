package ratelimit_test

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/ratelimit"
)

// simResult is the naive model's verdict for one operation.
type simResult struct {
	allowed    bool
	errCode    string
	limit      int64
	remaining  int64
	reset      int64
	retryAfter int64
	tier       string
}

type simTier struct {
	name string
	rank int64
	t    int64
	b    int64
}

type simRoute struct {
	scope string
	cost  int64
}

type simState struct {
	capacity int64
	tiers    map[string]simTier
	ranks    map[int64]string
	routes   map[string]simRoute
	tat      map[string]int64
	maxNow   int64
}

func ceilDiv(x, y int64) int64 {
	if x <= 0 {
		return 0
	}
	return (x + y - 1) / y
}

// simulate is a step-by-step rewrite of the specification rules.
func (s *simState) simulate(sub, path string, scopes []string, now int64) simResult {
	fail := func(code string) simResult { return simResult{errCode: code} }

	if sub == "" || path == "" {
		return fail("invalid")
	}
	for _, sc := range scopes {
		if sc == "" {
			return fail("invalid")
		}
	}
	if now < 0 || now > 1_000_000_000_000_000 {
		return fail("invalid_time")
	}
	if now < s.maxNow {
		return fail("rewind")
	}
	rt, ok := s.routes[path]
	if !ok {
		return fail("no_route")
	}

	var chosen *simTier
	for _, sc := range scopes {
		if len(sc) < 5 || sc[:5] != "tier:" {
			continue
		}
		if name, ok := s.tiers[sc[5:]]; ok {
			cp := name
			if chosen == nil || cp.rank > chosen.rank {
				chosen = &cp
			}
		}
	}
	if chosen == nil {
		var minName string
		var minRank int64
		first := true
		for name, tr := range s.tiers {
			if first || tr.rank < minRank {
				minRank, minName, first = tr.rank, name, false
			}
		}
		if first {
			return fail("no_tier")
		}
		cp := s.tiers[minName]
		chosen = &cp
	}

	if rt.scope != "" {
		granted := false
		for _, sc := range scopes {
			if sc == rt.scope {
				granted = true
				break
			}
		}
		if !granted {
			return fail("forbidden")
		}
	}
	if rt.cost > chosen.b {
		return fail("never")
	}

	prior, seen := s.tat[sub]
	if !seen {
		prior = -1
	}
	a := prior
	if a < now {
		a = now
	}
	newTAT := a + rt.cost*chosen.t
	burst := chosen.b * chosen.t

	active := int64(0)
	for sb, v := range s.tat {
		if v <= now {
			delete(s.tat, sb)
			if sb == sub {
				seen = false
			}
			continue
		}
		active++
	}
	if (!seen || s.tat[sub] <= now) && active >= s.capacity {
		return fail("full")
	}

	h := simResult{limit: chosen.b, tier: chosen.name}
	if newTAT-now <= burst {
		elapsed := newTAT - now
		h.allowed = true
		h.remaining = (burst - elapsed) / chosen.t
		h.reset = ceilDiv(elapsed, 1000)
		s.tat[sub] = newTAT
		s.maxNow = now
		return h
	}
	wait := a - now
	h.errCode = "limited"
	h.remaining = (burst - wait) / chosen.t
	if h.remaining < 0 {
		h.remaining = 0
	}
	h.reset = ceilDiv(wait, 1000)
	h.retryAfter = ceilDiv(newTAT-now-burst, 1000)
	return h
}

var errCodeMap = map[error]string{
	ratelimit.ErrInvalidArg:   "invalid",
	ratelimit.ErrInvalidTime:  "invalid_time",
	ratelimit.ErrClockRewind:  "rewind",
	ratelimit.ErrNoRoute:      "no_route",
	ratelimit.ErrNoTier:       "no_tier",
	ratelimit.ErrForbidden:    "forbidden",
	ratelimit.ErrNeverAllowed: "never",
	ratelimit.ErrTableFull:    "full",
	ratelimit.ErrRateLimited:  "limited",
}

// TestRandomVsNaive replays 2000 random operation sequences against the
// implementation and the naive simulation; inputs, outputs and the verdict
// rationale are logged for each operation.
func TestRandomVsNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261004))
	const runs = 2000
	const steps = 40
	for run := 0; run < runs; run++ {
		capacity := int64(1 + rng.Intn(3))
		l, err := ratelimit.New(capacity)
		if err != nil {
			t.Fatalf("run %d New: %v", run, err)
		}
		sim := &simState{
			capacity: capacity,
			tiers:    map[string]simTier{},
			ranks:    map[int64]string{},
			routes:   map[string]simRoute{},
			tat:      map[string]int64{},
		}

		tierNames := []string{"free", "pro", "team"}
		nTiers := 1 + rng.Intn(3)
		ranks := rng.Perm(1000)
		for i := 0; i < nTiers; i++ {
			name := tierNames[i]
			rank := int64(ranks[i] + 1)
			tt := int64(1 + rng.Intn(1000))
			bb := int64(1 + rng.Intn(5))
			if err := l.AddTier(name, rank, tt, bb); err != nil {
				t.Fatalf("run %d AddTier: %v", run, err)
			}
			sim.tiers[name] = simTier{name: name, rank: rank, t: tt, b: bb}
			sim.ranks[rank] = name
		}

		pathScopes := map[string]string{
			"/pub":    "",
			"/orders": "orders",
			"/bulk":   "bulk",
		}
		paths := []string{"/pub", "/orders", "/bulk", "/missing"}
		for p, sc := range pathScopes {
			cost := int64(1 + rng.Intn(6))
			if err := l.AddRoute(p, sc, cost); err != nil {
				t.Fatalf("run %d AddRoute: %v", run, err)
			}
			sim.routes[p] = simRoute{scope: sc, cost: cost}
		}

		now := int64(0)
		for k := 0; k < steps; k++ {
			sub := fmt.Sprintf("u%d", rng.Intn(4))
			path := paths[rng.Intn(len(paths))]
			pool := []string{"orders", "bulk", "other", "tier:free", "tier:pro", "tier:team", "tier:platinum", ""}
			var scopes []string
			for _, sc := range pool {
				if rng.Intn(3) == 0 {
					scopes = append(scopes, sc)
				}
			}
			if rng.Intn(6) == 0 {
				now = int64(rng.Intn(6000))
			} else {
				now += int64(rng.Intn(300))
			}
			if rng.Intn(20) == 0 {
				sub = ""
			}

			got := sim.simulate(sub, path, scopes, now)
			res, gerr := l.Allow(sub, path, scopes, now)
			wantCode := got.errCode
			gotCode := ""
			if gerr != nil {
				gotCode = errCodeMap[mapErr(gerr)]
			}
			t.Logf("run=%d step=%d in={sub:%q path:%q scopes:%v now:%d} impl={allowed:%v code:%s limit:%d remaining:%d reset:%d retryAfter:%d tier:%q} naive={allowed:%v code:%s limit:%d remaining:%d reset:%d retryAfter:%d tier:%q}",
				run, k, sub, path, scopes, now,
				res.Allowed, gotCode, res.Limit, res.Remaining, res.Reset, res.RetryAfter, res.Tier,
				got.allowed, wantCode, got.limit, got.remaining, got.reset, got.retryAfter, got.tier)

			if gotCode != wantCode {
				t.Fatalf("run %d step %d code mismatch: impl=%s naive=%s", run, k, gotCode, wantCode)
			}
			if gotCode == "" || gotCode == "limited" {
				if res.Allowed != got.allowed || res.Limit != got.limit || res.Remaining != got.remaining ||
					res.Reset != got.reset || res.RetryAfter != got.retryAfter || res.Tier != got.tier {
					t.Fatalf("run %d step %d headers mismatch:\nimpl=%+v\nnaive=%+v", run, k, res, got)
				}
			} else if res != (ratelimit.Result{}) {
				t.Fatalf("run %d step %d error carried headers: %+v", run, k, res)
			}
		}
	}
}

func mapErr(err error) error {
	for sentinel := range errCodeMap {
		if errors.Is(err, sentinel) {
			return sentinel
		}
	}
	return err
}
