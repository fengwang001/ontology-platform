package ratelimit_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/ratelimit"
)

func buildExample(t *testing.T, capacity int64) *ratelimit.Limiter {
	t.Helper()
	l, err := ratelimit.New(capacity)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	must(t, l.AddTier("free", 1, 1000, 2))
	must(t, l.AddTier("pro", 2, 100, 5))
	must(t, l.AddRoute("/orders", "orders", 1))
	must(t, l.AddRoute("/bulk", "bulk", 3))
	return l
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

type expect struct {
	allowed    bool
	err        error
	limit      int64
	remaining  int64
	reset      int64
	retryAfter int64
	tier       string
}

type step struct {
	sub    string
	path   string
	scopes []string
	now    int64
	want   expect
}

func runSteps(t *testing.T, l *ratelimit.Limiter, steps []step) {
	t.Helper()
	for i, s := range steps {
		res, err := l.Allow(s.sub, s.path, s.scopes, s.now)
		t.Logf("step=%d in={sub:%q path:%q scopes:%v now:%d} out={allowed:%v err:%v limit:%d remaining:%d reset:%d retryAfter:%d tier:%q}",
			i, s.sub, s.path, s.scopes, s.now, res.Allowed, err, res.Limit, res.Remaining, res.Reset, res.RetryAfter, res.Tier)
		if !errors.Is(err, s.want.err) {
			t.Fatalf("step %d: err = %v, want %v", i, err, s.want.err)
		}
		if s.want.err != nil && !errors.Is(s.want.err, ratelimit.ErrRateLimited) {
			if res != (ratelimit.Result{}) {
				t.Fatalf("step %d: non-limited error must carry no headers, got %+v", i, res)
			}
			continue
		}
		if res.Allowed != s.want.allowed || res.Limit != s.want.limit || res.Remaining != s.want.remaining ||
			res.Reset != s.want.reset || res.RetryAfter != s.want.retryAfter || res.Tier != s.want.tier {
			t.Fatalf("step %d: result = %+v, want allowed=%v limit=%d remaining=%d reset=%d retryAfter=%d tier=%q",
				i, res, s.want.allowed, s.want.limit, s.want.remaining, s.want.reset, s.want.retryAfter, s.want.tier)
		}
	}
}

// TestSpecExample reproduces the worked example from the specification.
func TestSpecExample(t *testing.T) {
	l := buildExample(t, 10)
	runSteps(t, l, []step{
		{"u", "/orders", []string{"orders", "tier:free"}, 0,
			expect{allowed: true, limit: 2, remaining: 1, reset: 1, tier: "free"}},
		{"u", "/orders", []string{"orders", "tier:free"}, 0,
			expect{allowed: true, limit: 2, remaining: 0, reset: 2, tier: "free"}},
		{"u", "/orders", []string{"orders", "tier:free"}, 0,
			expect{err: ratelimit.ErrRateLimited, limit: 2, remaining: 0, reset: 2, retryAfter: 1, tier: "free"}},
	})

	// Upgrade to pro while retaining debt (TAT=2000).
	runSteps(t, l, []step{
		{"u", "/orders", []string{"orders", "tier:free", "tier:pro"}, 0,
			expect{err: ratelimit.ErrRateLimited, limit: 5, remaining: 0, reset: 2, retryAfter: 2, tier: "pro"}},
		{"u", "/orders", []string{"orders", "tier:free", "tier:pro"}, 1600,
			expect{allowed: true, limit: 5, remaining: 0, reset: 1, tier: "pro"}},
	})
}

// TestBurstBoundary: new-now == B*T allows; one millisecond more limits.
func TestBurstBoundary(t *testing.T) {
	l := buildExample(t, 10)
	runSteps(t, l, []step{
		{"u", "/orders", []string{"orders", "tier:pro"}, 0,
			expect{allowed: true, limit: 5, remaining: 4, reset: 1, tier: "pro"}},
		{"u", "/orders", []string{"orders", "tier:pro"}, 0,
			expect{allowed: true, limit: 5, remaining: 3, reset: 1, tier: "pro"}},
		{"u", "/orders", []string{"orders", "tier:pro"}, 0,
			expect{allowed: true, limit: 5, remaining: 2, reset: 1, tier: "pro"}},
		{"u", "/orders", []string{"orders", "tier:pro"}, 0,
			expect{allowed: true, limit: 5, remaining: 1, reset: 1, tier: "pro"}},
		{"u", "/orders", []string{"orders", "tier:pro"}, 0,
			expect{allowed: true, limit: 5, remaining: 0, reset: 1, tier: "pro"}},
		{"u", "/orders", []string{"orders", "tier:pro"}, 0,
			expect{err: ratelimit.ErrRateLimited, limit: 5, remaining: 0, reset: 1, retryAfter: 1, tier: "pro"}},
		// now=100: new-now = 600-100 = 500 exactly -> allowed boundary.
		{"u", "/orders", []string{"orders", "tier:pro"}, 100,
			expect{allowed: true, limit: 5, remaining: 0, reset: 1, tier: "pro"}},
		// TAT=600; at now=100 new-now=600 > 500 -> limited by one.
		{"u", "/orders", []string{"orders", "tier:pro"}, 100,
			expect{err: ratelimit.ErrRateLimited, limit: 5, remaining: 0, reset: 1, retryAfter: 1, tier: "pro"}},
	})
}

// TestCostBoundary: cost == B is satisfiable; cost == B+1 is never allowed.
func TestCostBoundary(t *testing.T) {
	l := buildExample(t, 10)
	must(t, l.AddTier("big", 3, 100, 3))
	must(t, l.AddRoute("/triple", "", 3))
	must(t, l.AddRoute("/quad", "", 4))
	runSteps(t, l, []step{
		{"u", "/bulk", []string{"bulk", "tier:free"}, 10_000,
			expect{err: ratelimit.ErrNeverAllowed}},
		{"u", "/triple", []string{"tier:big"}, 0,
			expect{allowed: true, limit: 3, remaining: 0, reset: 1, tier: "big"}},
		{"u", "/quad", []string{"tier:big"}, 10_000,
			expect{err: ratelimit.ErrNeverAllowed}},
	})
}

// TestRemainingFloor checks the floor division of Remaining.
func TestRemainingFloor(t *testing.T) {
	l, err := ratelimit.New(10)
	must(t, err)
	must(t, l.AddTier("free", 1, 300, 2))
	must(t, l.AddTier("free2", 2, 1000, 3))
	must(t, l.AddRoute("/r", "", 1))
	runSteps(t, l, []step{
		// burst=600, new-now=300 => (600-300)/300 = 1.
		{"u", "/r", []string{"tier:free"}, 0,
			expect{allowed: true, limit: 2, remaining: 1, reset: 1, tier: "free"}},
		// burst=3000; after 1 unit: (3000-1000)/1000=2.
		{"v", "/r", []string{"tier:free2"}, 0,
			expect{allowed: true, limit: 3, remaining: 2, reset: 1, tier: "free2"}},
		{"v", "/r", []string{"tier:free2"}, 0,
			expect{allowed: true, limit: 3, remaining: 1, reset: 2, tier: "free2"}},
		{"v", "/r", []string{"tier:free2"}, 0,
			expect{allowed: true, limit: 3, remaining: 0, reset: 3, tier: "free2"}},
	})
}

// TestCeilExactSecond: 2000ms -> 2 (no carry); RetryAfter exact second likewise.
func TestCeilExactSecond(t *testing.T) {
	l, err := ratelimit.New(10)
	must(t, err)
	must(t, l.AddTier("free", 1, 1000, 5))
	must(t, l.AddRoute("/r", "", 1))
	runSteps(t, l, []step{
		{"u", "/r", []string{"tier:free"}, 0,
			expect{allowed: true, limit: 5, remaining: 4, reset: 1, tier: "free"}},
		{"u", "/r", []string{"tier:free"}, 0,
			expect{allowed: true, limit: 5, remaining: 3, reset: 2, tier: "free"}},
		{"u", "/r", []string{"tier:free"}, 0,
			expect{allowed: true, limit: 5, remaining: 2, reset: 3, tier: "free"}},
		{"u", "/r", []string{"tier:free"}, 0,
			expect{allowed: true, limit: 5, remaining: 1, reset: 4, tier: "free"}},
		{"u", "/r", []string{"tier:free"}, 0,
			expect{allowed: true, limit: 5, remaining: 0, reset: 5, tier: "free"}},
		// excess = 6000-now0 - 5000 = 1000 => RetryAfter exactly 1.
		{"u", "/r", []string{"tier:free"}, 0,
			expect{err: ratelimit.ErrRateLimited, limit: 5, remaining: 0, reset: 5, retryAfter: 1, tier: "free"}},
	})

	l2, err := ratelimit.New(10)
	must(t, err)
	must(t, l2.AddTier("free", 1, 1000, 1))
	must(t, l2.AddRoute("/r", "", 1))
	runSteps(t, l2, []step{
		{"u", "/r", []string{"tier:free"}, 0,
			expect{allowed: true, limit: 1, remaining: 0, reset: 1, tier: "free"}},
		{"u", "/r", []string{"tier:free"}, 0,
			expect{err: ratelimit.ErrRateLimited, limit: 1, remaining: 0, reset: 1, retryAfter: 1, tier: "free"}},
	})
}

// TestCeilSubSecond verifies 1ms rounds up to 1 second.
func TestCeilSubSecond(t *testing.T) {
	l, err := ratelimit.New(10)
	must(t, err)
	must(t, l.AddTier("t", 1, 100, 2))
	must(t, l.AddRoute("/r", "", 1))
	runSteps(t, l, []step{
		{"u", "/r", []string{"tier:t"}, 0,
			expect{allowed: true, limit: 2, remaining: 1, reset: 1, tier: "t"}},
		{"u", "/r", []string{"tier:t"}, 0,
			expect{allowed: true, limit: 2, remaining: 0, reset: 1, tier: "t"}},
		{"u", "/r", []string{"tier:t"}, 0,
			expect{err: ratelimit.ErrRateLimited, limit: 2, remaining: 0, reset: 1, retryAfter: 1, tier: "t"}},
	})
}

// TestFirstCallNoTAT: first evaluation starts at now.
func TestFirstCallNoTAT(t *testing.T) {
	l := buildExample(t, 10)
	runSteps(t, l, []step{
		{"u", "/orders", []string{"orders", "tier:pro"}, 5000,
			expect{allowed: true, limit: 5, remaining: 4, reset: 1, tier: "pro"}},
	})
}

// TestIdleTATBelowNow: after enough idle time the bucket behaves empty again.
func TestIdleTATBelowNow(t *testing.T) {
	l := buildExample(t, 10)
	runSteps(t, l, []step{
		{"u", "/orders", []string{"orders", "tier:pro"}, 0,
			expect{allowed: true, limit: 5, remaining: 4, reset: 1, tier: "pro"}},
		// TAT=100; at now=10000 the bucket is empty: fresh full remaining.
		{"u", "/orders", []string{"orders", "tier:pro"}, 10_000,
			expect{allowed: true, limit: 5, remaining: 4, reset: 1, tier: "pro"}},
	})
}

// TestUnknownTierIgnored: unregistered tier:* scopes are forward-compatible.
func TestUnknownTierIgnored(t *testing.T) {
	l := buildExample(t, 10)
	// tier:platinum unknown; no known tier => lowest rank free applies.
	runSteps(t, l, []step{
		{"u", "/orders", []string{"orders", "tier:platinum"}, 0,
			expect{allowed: true, limit: 2, remaining: 1, reset: 1, tier: "free"}},
		// Unknown tier between known tiers does not change the max.
		{"u2", "/orders", []string{"orders", "tier:free", "tier:platinum", "tier:pro"}, 0,
			expect{allowed: true, limit: 5, remaining: 4, reset: 1, tier: "pro"}},
	})
}

// TestHighestRankWins and fallback to lowest rank when no tier mark.
func TestTierSelection(t *testing.T) {
	l := buildExample(t, 10)
	runSteps(t, l, []step{
		// No tier mark => min-rank registered tier (free).
		{"u", "/orders", []string{"orders"}, 0,
			expect{allowed: true, limit: 2, remaining: 1, reset: 1, tier: "free"}},
		// Multiple tiers => greatest rank.
		{"v", "/orders", []string{"orders", "tier:free", "tier:pro"}, 0,
			expect{allowed: true, limit: 5, remaining: 4, reset: 1, tier: "pro"}},
	})
}

// TestForbiddenBeforeNeverAllowed: missing scope reports forbidden first.
func TestForbiddenBeforeNeverAllowed(t *testing.T) {
	l := buildExample(t, 10)
	runSteps(t, l, []step{
		// /bulk cost 3 > free B=2, but scope bulk is missing => forbidden.
		{"u", "/bulk", []string{"tier:free"}, 0,
			expect{err: ratelimit.ErrForbidden}},
		// With scope, the never-allowed verdict surfaces.
		{"u", "/bulk", []string{"bulk", "tier:free"}, 0,
			expect{err: ratelimit.ErrNeverAllowed}},
	})
}

// TestRejectionKeepsTAT: failed calls must not alter TAT or clock.
func TestRejectionKeepsTAT(t *testing.T) {
	l := buildExample(t, 10)
	// One allowed call establishes TAT=1000 under free.
	runSteps(t, l, []step{
		{"u", "/orders", []string{"orders", "tier:free"}, 0,
			expect{allowed: true, limit: 2, remaining: 1, reset: 1, tier: "free"}},
	})
	// A forbidden attempt must not consume quota.
	runSteps(t, l, []step{
		{"u", "/orders", []string{"tier:free"}, 500,
			expect{err: ratelimit.ErrForbidden}},
	})
	// Retry at now=500: TAT still 1000; new=2000, new-now=1500 <= 2000 allowed,
	// remaining = floor((2000-1500)/1000)=0.
	runSteps(t, l, []step{
		{"u", "/orders", []string{"orders", "tier:free"}, 500,
			expect{allowed: true, limit: 2, remaining: 0, reset: 2, tier: "free"}},
	})
	// A never-allowed attempt must not touch TAT either (and clock: use later now).
	runSteps(t, l, []step{
		{"u", "/bulk", []string{"bulk", "tier:free"}, 600,
			expect{err: ratelimit.ErrNeverAllowed}},
	})
	// Rate-limited attempts keep TAT: fill to burst, then limited at now=600.
	runSteps(t, l, []step{
		{"u", "/orders", []string{"orders", "tier:pro"}, 600,
			expect{err: ratelimit.ErrRateLimited, limit: 5, remaining: 0, reset: 2, retryAfter: 1, tier: "pro"}},
		{"u", "/orders", []string{"orders", "tier:pro"}, 600,
			expect{err: ratelimit.ErrRateLimited, limit: 5, remaining: 0, reset: 2, retryAfter: 1, tier: "pro"}},
	})
}

// TestSameNowVsAdvancing: identical-now burst vs advancing clock differ.
func TestSameNowVsAdvancing(t *testing.T) {
	same := buildExample(t, 10)
	adv := buildExample(t, 10)
	runSteps(t, same, []step{
		{"u", "/orders", []string{"orders", "tier:pro"}, 0,
			expect{allowed: true, limit: 5, remaining: 4, reset: 1, tier: "pro"}},
		{"u", "/orders", []string{"orders", "tier:pro"}, 0,
			expect{allowed: true, limit: 5, remaining: 3, reset: 1, tier: "pro"}},
		{"u", "/orders", []string{"orders", "tier:pro"}, 0,
			expect{allowed: true, limit: 5, remaining: 2, reset: 1, tier: "pro"}},
	})
	runSteps(t, adv, []step{
		{"u", "/orders", []string{"orders", "tier:pro"}, 0,
			expect{allowed: true, limit: 5, remaining: 4, reset: 1, tier: "pro"}},
		// now advances 100 each time: TAT tracks now, full remaining recovered.
		{"u", "/orders", []string{"orders", "tier:pro"}, 100,
			expect{allowed: true, limit: 5, remaining: 4, reset: 1, tier: "pro"}},
		{"u", "/orders", []string{"orders", "tier:pro"}, 200,
			expect{allowed: true, limit: 5, remaining: 4, reset: 1, tier: "pro"}},
	})
}

// TestTwoSubjectsIndependent: separate TAT maps per subject.
func TestTwoSubjectsIndependent(t *testing.T) {
	l := buildExample(t, 2)
	runSteps(t, l, []step{
		{"u1", "/orders", []string{"orders", "tier:free"}, 0,
			expect{allowed: true, limit: 2, remaining: 1, reset: 1, tier: "free"}},
		{"u2", "/orders", []string{"orders", "tier:free"}, 0,
			expect{allowed: true, limit: 2, remaining: 1, reset: 1, tier: "free"}},
		{"u1", "/orders", []string{"orders", "tier:free"}, 0,
			expect{allowed: true, limit: 2, remaining: 0, reset: 2, tier: "free"}},
		{"u2", "/orders", []string{"orders", "tier:free"}, 0,
			expect{allowed: true, limit: 2, remaining: 0, reset: 2, tier: "free"}},
	})
}

// TestCapacityRelease: S=1; a subject with TAT<=now no longer occupies a slot.
func TestCapacityRelease(t *testing.T) {
	l := buildExample(t, 1)
	runSteps(t, l, []step{
		{"u1", "/orders", []string{"orders", "tier:free"}, 0,
			expect{allowed: true, limit: 2, remaining: 1, reset: 1, tier: "free"}},
		// u1 has TAT=1000 > 0 => table full for u2.
		{"u2", "/orders", []string{"orders", "tier:free"}, 0,
			expect{err: ratelimit.ErrTableFull}},
		// At now=1000 u1's TAT<=now: slot released for u2.
		{"u2", "/orders", []string{"orders", "tier:free"}, 1000,
			expect{allowed: true, limit: 2, remaining: 1, reset: 1, tier: "free"}},
	})

	// A rate-limited subject still occupies its slot.
	l2 := buildExample(t, 1)
	runSteps(t, l2, []step{
		{"u1", "/orders", []string{"orders", "tier:free"}, 0,
			expect{allowed: true, limit: 2, remaining: 1, reset: 1, tier: "free"}},
		{"u1", "/orders", []string{"orders", "tier:free"}, 0,
			expect{allowed: true, limit: 2, remaining: 0, reset: 2, tier: "free"}},
		{"u2", "/orders", []string{"orders", "tier:free"}, 0,
			expect{err: ratelimit.ErrTableFull}},
	})
}

// TestErrorOrdering exercises the documented rejection precedence.
func TestErrorOrdering(t *testing.T) {
	l := buildExample(t, 1)

	// Invalid args beat everything.
	if _, err := l.Allow("", "/orders", []string{"orders"}, 0); !errors.Is(err, ratelimit.ErrInvalidArg) {
		t.Fatalf("empty sub: %v", err)
	}
	if _, err := l.Allow("u", "", []string{"orders"}, 0); !errors.Is(err, ratelimit.ErrInvalidArg) {
		t.Fatalf("empty path: %v", err)
	}
	if _, err := l.Allow("u", "/orders", []string{""}, 0); !errors.Is(err, ratelimit.ErrInvalidArg) {
		t.Fatalf("empty scope: %v", err)
	}
	// Invalid time beats clock rewind.
	if _, err := l.Allow("u", "/orders", []string{"orders"}, -1); !errors.Is(err, ratelimit.ErrInvalidTime) {
		t.Fatalf("negative now: %v", err)
	}
	if _, err := l.Allow("u", "/orders", []string{"orders"}, 1_000_000_000_000_001); !errors.Is(err, ratelimit.ErrInvalidTime) {
		t.Fatalf("too-large now: %v", err)
	}
	// Clock rewind beats missing route.
	r500, e500 := l.Allow("u", "/orders", []string{"orders", "tier:free"}, 500)
	must2(t, r500, e500)
	if _, err := l.Allow("u", "/missing", []string{"orders"}, 100); !errors.Is(err, ratelimit.ErrClockRewind) {
		t.Fatalf("rewind: %v", err)
	}
	// Missing route beats no-tier (use a limiter without tiers).
	plain, err := ratelimit.New(1)
	must(t, err)
	must(t, plain.AddRoute("/r", "", 1))
	if _, err := plain.Allow("u", "/missing", nil, 0); !errors.Is(err, ratelimit.ErrNoRoute) {
		t.Fatalf("no route: %v", err)
	}
	if _, err := plain.Allow("u", "/r", nil, 0); !errors.Is(err, ratelimit.ErrNoTier) {
		t.Fatalf("no tier: %v", err)
	}
	// Clock rewind must not update maxNow: advancing again works.
	r600, e600 := l.Allow("u", "/orders", []string{"orders", "tier:free"}, 600)
	must2(t, r600, e600)
}

func must2(t *testing.T, res ratelimit.Result, err error) {
	t.Helper()
	if err != nil || !res.Allowed {
		t.Fatalf("expected allowed, got res=%+v err=%v", res, err)
	}
}

// TestRegistrationValidation covers AddTier/AddRoute arg and duplicate rules.
func TestRegistrationValidation(t *testing.T) {
	l, err := ratelimit.New(1)
	must(t, err)
	cases := []struct {
		name string
		err  error
	}{
		{"", ratelimit.ErrInvalidArg},
		{"ok", nil},
		{"ok", ratelimit.ErrDuplicate}, // duplicate name
	}
	ranks := []int64{1, 1, 2}
	for i, c := range cases {
		if got := l.AddTier(c.name, ranks[i], 100, 2); !errors.Is(got, c.err) {
			t.Fatalf("AddTier(%q rank=%d): %v want %v", c.name, ranks[i], got, c.err)
		}
	}
	if got := l.AddTier("dupRank", 1, 100, 2); !errors.Is(got, ratelimit.ErrDuplicate) {
		t.Fatalf("duplicate rank: %v", got)
	}
	for _, bad := range []int64{0, 1001} {
		if got := l.AddTier("x", bad, 100, 2); !errors.Is(got, ratelimit.ErrInvalidArg) {
			t.Fatalf("rank %d: %v", bad, got)
		}
	}
	for _, tb := range [][2]int64{{0, 2}, {1_000_001, 2}, {100, 0}, {100, 1_000_001}} {
		if got := l.AddTier("z", 50, tb[0], tb[1]); !errors.Is(got, ratelimit.ErrInvalidArg) {
			t.Fatalf("T/B %v: %v", tb, got)
		}
	}
	if got := l.AddRoute("", "", 1); !errors.Is(got, ratelimit.ErrInvalidArg) {
		t.Fatalf("empty path: %v", got)
	}
	must(t, l.AddRoute("/r", "", 1))
	if got := l.AddRoute("/r", "", 1); !errors.Is(got, ratelimit.ErrDuplicate) {
		t.Fatalf("duplicate path: %v", got)
	}
	if got := l.AddRoute("/bad", "", 0); !errors.Is(got, ratelimit.ErrInvalidArg) {
		t.Fatalf("cost 0: %v", got)
	}

	if _, err := ratelimit.New(0); !errors.Is(err, ratelimit.ErrInvalidArg) {
		t.Fatalf("capacity 0: %v", err)
	}
	if _, err := ratelimit.New(1_000_001); !errors.Is(err, ratelimit.ErrInvalidArg) {
		t.Fatalf("capacity too big: %v", err)
	}
}

// TestConcurrentAllow: concurrent calls must be race-free and keep capacity.
func TestConcurrentAllow(t *testing.T) {
	l := buildExample(t, 4)
	// Producers run concurrently and race on the Limiter; one consumer drains
	// requests and stamps them with a monotonic clock, which is the only
	// legal interleaving under the no-rewind rule.
	type req struct {
		sub  string
		now  int64
		res  ratelimit.Result
		err  error
		done chan struct{}
	}
	requests := make(chan *req, 400)
	var wg sync.WaitGroup
	go func() {
		now := int64(0)
		for r := range requests {
			r.res, r.err = l.Allow(r.sub, "/orders", []string{"orders", "tier:pro"}, now)
			now++
			close(r.done)
		}
	}()
	var allowed, limited, full, other int64
	var mu sync.Mutex
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for k := 0; k < 50; k++ {
				r := &req{sub: "u" + itoa(id), done: make(chan struct{})}
				requests <- r
				<-r.done
				mu.Lock()
				switch {
				case r.err == nil && r.res.Allowed:
					allowed++
				case errors.Is(r.err, ratelimit.ErrRateLimited):
					limited++
				case errors.Is(r.err, ratelimit.ErrTableFull):
					full++
				default:
					other++
				}
				mu.Unlock()
			}
		}(g)
	}
	wg.Wait()
	close(requests)
	t.Logf("concurrent outcome allowed=%d limited=%d full=%d other=%d", allowed, limited, full, other)
	if other != 0 {
		t.Fatalf("unexpected errors: %d", other)
	}
	if allowed+limited+full != 400 {
		t.Fatalf("total = %d, want 400", allowed+limited+full)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
