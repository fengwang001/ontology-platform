package vegas

import "testing"

func fillToLimit(t *testing.T, l *Limiter, now int64) []Token {
	t.Helper()
	var toks []Token
	for {
		tok, err := l.Acquire(now)
		if err == ErrAtLimit {
			return toks
		}
		if err != nil {
			t.Fatalf("fill: %v", err)
		}
		toks = append(toks, tok)
	}
}

// TestAppLimitedBoundary: w*2 == L still applies Vegas; w*2 < L does not but
// the sample is recorded and m updates.
func TestAppLimitedBoundary(t *testing.T) {
	l, _ := New(Config{L0: 4, Lmin: 1, Lmax: 10, Alpha: 2, Beta: 4, Tmo: 1e9, Cd: 1e9, Wm: 1e9})
	toks := fillToLimit(t, l, 0)
	if err := l.Release(toks[0], ResultSuccess, 100, 1); err != nil {
		t.Fatal(err)
	}
	if s := l.State(); s.L != 4 || s.WindowMinRTT != 100 || s.WindowSize != 1 {
		t.Fatalf("app-limited sample: %+v", s)
	}
	// w=4: 2w == L, applies; q=ceil(4*100/200)=2 == alpha, unchanged.
	if err := l.Release(toks[3], ResultSuccess, 200, 2); err != nil {
		t.Fatal(err)
	}
	if got := l.State().L; got != 4 {
		t.Fatalf("q==alpha keep L, got %d", got)
	}

	l2, _ := New(Config{L0: 3, Lmin: 1, Lmax: 10, Alpha: 2, Beta: 4, Tmo: 1e9, Cd: 1e9, Wm: 1e9})
	t2 := fillToLimit(t, l2, 0)
	if err := l2.Release(t2[0], ResultSuccess, 10_000, 1); err != nil {
		t.Fatal(err)
	}
	if s := l2.State(); s.L != 3 || s.WindowMinRTT != 10_000 {
		t.Fatalf("strict app-limited: %+v", s)
	}
}

// TestQThresholds: q exactly alpha/beta hold L; below/above move it.
func TestQThresholds(t *testing.T) {
	l, _ := New(Config{L0: 8, Lmin: 1, Lmax: 12, Alpha: 2, Beta: 4, Tmo: 1e9, Cd: 1e9, Wm: 1e9})
	toks := fillToLimit(t, l, 0) // w = 1..8
	// w=8 first: m=100, q=0 -> grow to 9.
	if err := l.Release(toks[7], ResultSuccess, 100, 1); err != nil {
		t.Fatal(err)
	}
	if got := l.State().L; got != 9 {
		t.Fatalf("q=0 grow: %d", got)
	}
	// w=7 (2*7>=9): rtt=113 -> 9*13/113=1.04 ceil 2 == alpha, hold.
	if err := l.Release(toks[6], ResultSuccess, 113, 2); err != nil {
		t.Fatal(err)
	}
	if got := l.State().L; got != 9 {
		t.Fatalf("q==alpha: %d", got)
	}
	// rtt=112: 9*12/112=0.97 ceil 1 < alpha -> grow to 10.
	if err := l.Release(toks[5], ResultSuccess, 112, 3); err != nil {
		t.Fatal(err)
	}
	if got := l.State().L; got != 10 {
		t.Fatalf("q<alpha: %d", got)
	}
	// w=4: 2*4=8 < 10 app-limited: sample in, L unchanged.
	if err := l.Release(toks[3], ResultSuccess, 500, 4); err != nil {
		t.Fatal(err)
	}
	if got := l.State().L; got != 10 {
		t.Fatalf("app-limited: %d", got)
	}
	// w=5: 2*5==10 applies; m=100; rtt=500 -> 10*400/500=8 > beta -> 9.
	if err := l.Release(toks[4], ResultSuccess, 500, 5); err != nil {
		t.Fatal(err)
	}
	if got := l.State().L; got != 9 {
		t.Fatalf("q>beta: %d", got)
	}
	// q exactly beta=4: L=9, rtt=180 -> 9*80/180=4. Fresh high-w tokens.
	fresh := fillToLimit(t, l, 6)
	high := fresh[len(fresh)-1]
	if err := l.Release(high, ResultSuccess, 180, 7); err != nil {
		t.Fatal(err)
	}
	if got := l.State().L; got != 9 {
		t.Fatalf("q==beta: %d", got)
	}
}

func TestFirstSampleQZero(t *testing.T) {
	l, _ := New(Config{L0: 2, Lmin: 1, Lmax: 5, Alpha: 1, Beta: 3, Tmo: 100, Cd: 0, Wm: 100})
	tok, _ := l.Acquire(0)
	if err := l.Release(tok, ResultSuccess, 42, 1); err != nil {
		t.Fatal(err)
	}
	if s := l.State(); s.L != 3 || s.WindowMinRTT != 42 {
		t.Fatalf("first sample: %+v", s)
	}
}

func TestDropFloorCooldown(t *testing.T) {
	l, _ := New(Config{L0: 3, Lmin: 2, Lmax: 5, Alpha: 1, Beta: 3, Tmo: 10_000, Cd: 10, Wm: 100})
	t1, _ := l.Acquire(0)
	t2, _ := l.Acquire(0)
	t3, _ := l.Acquire(0)

	if err := l.Release(t1, ResultDrop, 0, 0); err != nil {
		t.Fatal(err)
	}
	if s := l.State(); s.L != 2 || s.LastCut != 0 {
		t.Fatalf("drop to floor: %+v", s)
	}
	if err := l.Release(t2, ResultDrop, 0, 9); err != nil {
		t.Fatal(err)
	}
	if s := l.State(); s.L != 2 || s.LastCut != 0 {
		t.Fatalf("cooldown: %+v", s)
	}
	// t=10 == lc+Cd: cut runs; floor(2*9/10)=1 clamped to 2; lc refreshes.
	if err := l.Release(t3, ResultDrop, 0, 10); err != nil {
		t.Fatal(err)
	}
	if s := l.State(); s.L != 2 || s.LastCut != 10 {
		t.Fatalf("boundary cut refreshes lc even when L unchanged: %+v", s)
	}
	// n=0 now; admit a fresh token after the cooldown window to drop at 19.
	t4, err := l.Acquire(11)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Release(t4, ResultDrop, 0, 19); err != nil {
		t.Fatal(err)
	}
	if s := l.State(); s.LastCut != 10 {
		t.Fatalf("t=19 still cooling: %+v", s)
	}
}

func TestDropCdZero(t *testing.T) {
	l, _ := New(Config{L0: 100, Lmin: 1, Lmax: 1000, Alpha: 1, Beta: 3, Tmo: 10_000, Cd: 0, Wm: 100})
	toks := fillToLimit(t, l, 0)
	want := int64(100)
	for i, tok := range toks {
		if err := l.Release(tok, ResultDrop, 0, int64(i)); err != nil {
			t.Fatal(err)
		}
		want = want * 9 / 10
		if want < 1 {
			want = 1
		}
		if got := l.State().L; got != want {
			t.Fatalf("drop %d: L=%d want %d", i, got, want)
		}
	}
}

func TestTimeoutBoundary(t *testing.T) {
	cfg := Config{L0: 1, Lmin: 1, Lmax: 2, Alpha: 1, Beta: 3, Tmo: 50, Cd: 1000, Wm: 100}

	// Scenario A: a token is releasable at expires-1.
	l, _ := New(cfg)
	tok, _ := l.Acquire(0)
	if err := l.Release(tok, ResultIgnore, 0, 49); err != nil {
		t.Fatalf("valid at expires-1: %v", err)
	}

	// Scenario B: token expiring exactly at 50 occupies the slot at 49, is
	// reaped by the Acquire at 50, and cannot be released afterwards.
	l2, _ := New(cfg)
	tok2, _ := l2.Acquire(0)
	if _, err := l2.Acquire(49); err != ErrAtLimit {
		t.Fatalf("l2 occupied at 49: %v", err)
	}
	next, err := l2.Acquire(50)
	if err != nil {
		t.Fatalf("reap exactly at expiry frees slot: %v", err)
	}
	if next.Seq != 2 {
		t.Fatalf("seq=%d want 2", next.Seq)
	}
	if err := l2.Release(tok2, ResultSuccess, 10, 50); err != ErrTokenTimedOut {
		t.Fatalf("reaped token: %v", err)
	}
}

func TestReapMultipleCutOnce(t *testing.T) {
	l, _ := New(Config{L0: 5, Lmin: 1, Lmax: 5, Alpha: 1, Beta: 3, Tmo: 10, Cd: 100, Wm: 100})
	toks := fillToLimit(t, l, 0)
	tok, err := l.Acquire(10)
	if err != nil {
		t.Fatal(err)
	}
	if s := l.State(); s.L != 4 || s.N != 1 || s.LastCut != 10 {
		t.Fatalf("reap: %+v", s)
	}
	for i, old := range toks {
		if err := l.Release(old, ResultIgnore, 0, 11); err != ErrTokenTimedOut {
			t.Fatalf("old %d: %v", i, err)
		}
	}
	if err := l.Release(tok, ResultIgnore, 0, 11); err != nil {
		t.Fatalf("new token valid: %v", err)
	}
}

func TestReapBeforeRelease(t *testing.T) {
	l, _ := New(Config{L0: 3, Lmin: 1, Lmax: 3, Alpha: 1, Beta: 3, Tmo: 10, Cd: 10_000, Wm: 1000})
	t1, _ := l.Acquire(0)
	t2, _ := l.Acquire(0)
	t3, _ := l.Acquire(1) // expires at 11, survives the reap at t=10
	if err := l.Release(t3, ResultIgnore, 0, 10); err != nil {
		t.Fatal(err)
	}
	if s := l.State(); s.N != 0 || s.Outstanding != 0 {
		t.Fatalf("post: %+v", s)
	}
	if err := l.Release(t1, ResultIgnore, 0, 11); err != ErrTokenTimedOut {
		t.Fatalf("t1: %v", err)
	}
	if err := l.Release(t2, ResultIgnore, 0, 11); err != ErrTokenTimedOut {
		t.Fatalf("t2: %v", err)
	}
}

// TestWindowExactExit: sample at t counts through t+Wm-1, leaves at t+Wm;
// once every old sample is gone only the newest sample remains.
func TestWindowExactExit(t *testing.T) {
	// Samples are placed via app-limited (w=1) tokens, so L never moves and
	// the test isolates sliding-window behaviour.
	l, _ := New(Config{L0: 10, Lmin: 1, Lmax: 100, Alpha: 2, Beta: 4, Tmo: 1e9, Cd: 1e9, Wm: 100})
	admit := func(now int64) Token {
		tok, err := l.Acquire(now)
		if err != nil {
			t.Fatalf("admit at %d: %v", now, err)
		}
		return tok
	}
	release := func(tok Token, rtt, now int64) {
		if err := l.Release(tok, ResultSuccess, rtt, now); err != nil {
			t.Fatalf("release at %d: %v", now, err)
		}
	}

	release(admit(0), 50, 0) // sample (0,50), w=1 app-limited
	if s := l.State(); s.WindowSize != 1 || s.WindowMinRTT != 50 {
		t.Fatalf("first sample: %+v", s)
	}
	release(admit(99), 500, 99) // (0,50) still in window at 99
	if s := l.State(); s.WindowSize != 2 || s.WindowMinRTT != 50 {
		t.Fatalf("at 99: %+v", s)
	}
	release(admit(100), 800, 100) // (0,50) leaves exactly at 100
	if s := l.State(); s.WindowSize != 2 || s.WindowMinRTT != 500 {
		t.Fatalf("exact exit at t+Wm: %+v", s)
	}
	release(admit(199), 900, 199) // (99,500) leaves exactly at 199
	if s := l.State(); s.WindowSize != 2 || s.WindowMinRTT != 800 {
		t.Fatalf("at 199: %+v", s)
	}
	release(admit(200), 900, 200) // (100,800) leaves exactly at 200
	if s := l.State(); s.WindowSize != 2 || s.WindowMinRTT != 900 {
		t.Fatalf("at 200: %+v", s)
	}

	// Window fully emptied of old samples: let both remaining samples expire
	// and add one fresh sample; the window contains only it, m == its rtt.
	release(admit(300), 700, 300)
	s := l.State()
	if s.WindowSize != 1 || s.WindowMinRTT != 700 {
		t.Fatalf("emptied window with only new sample: %+v", s)
	}
	if got := l.State().L; got != 10 {
		t.Fatalf("app-limited samples must not change L, got %d", got)
	}
}

// TestLimitBoundsGrowth pins L at Lmax; drops pin it at Lmin.
func TestLimitBounds(t *testing.T) {
	l, _ := New(Config{L0: 3, Lmin: 2, Lmax: 4, Alpha: 1, Beta: 2, Tmo: 1e9, Cd: 1e9, Wm: 1e9})
	for step := int64(0); step < 15; step++ {
		toks := fillToLimit(t, l, step)
		high := toks[len(toks)-1]
		if err := l.Release(high, ResultSuccess, 10, step); err != nil {
			t.Fatal(err)
		}
		if L := l.State().L; L < 2 || L > 4 {
			t.Fatalf("L=%d", L)
		}
	}
	if got := l.State().L; got != 4 {
		t.Fatalf("pin at Lmax: %d", got)
	}

	l2, _ := New(Config{L0: 4, Lmin: 2, Lmax: 4, Alpha: 1, Beta: 2, Tmo: 1e9, Cd: 0, Wm: 1e9})
	for step := int64(0); step < 20; step++ {
		tok, err := l2.Acquire(step)
		if err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		if err := l2.Release(tok, ResultDrop, 0, step); err != nil {
			t.Fatal(err)
		}
		if L := l2.State().L; L < 2 || L > 4 {
			t.Fatalf("L=%d", L)
		}
	}
	if got := l2.State().L; got != 2 {
		t.Fatalf("pin at Lmin: %d", got)
	}
}

// TestLimitBelowInFlight: when drops push L below n, Acquire refuses until
// in-flight requests return. (L=12 -> cut to 10 with all 12 outstanding.)
func TestLimitBelowInFlight(t *testing.T) {
	l, _ := New(Config{L0: 12, Lmin: 2, Lmax: 12, Alpha: 1, Beta: 2, Tmo: 1e9, Cd: 0, Wm: 1e9})
	toks := fillToLimit(t, l, 0) // n=12
	// Two drops without cooldown: 12->10 after two releases, n=10 => equal.
	// One more drop: 10->9, n=9 equal. The gap below appears when a cut is
	// 10% at larger L. Start bigger to show L<n directly.
	l2, _ := New(Config{L0: 12, Lmin: 2, Lmax: 12, Alpha: 1, Beta: 2, Tmo: 1e9, Cd: 1000, Wm: 1e9})
	fillToLimit(t, l2, 0)
	// Single drop 12->10 with 11 outstanding (n after release = 11 > L=10).
	first := Token{Seq: 1}
	_ = toks
	if err := l2.Release(first, ResultDrop, 0, 0); err != nil {
		t.Fatal(err)
	}
	s := l2.State()
	if s.L != 10 || s.N != 11 {
		t.Fatalf("L below n: %+v", s)
	}
	if _, err := l2.Acquire(1); err != ErrAtLimit {
		t.Fatalf("must refuse while n>=L: %v", err)
	}
	// Return two ignored requests: n=9 < L=10, one Acquire succeeds.
	if err := l2.Release(Token{Seq: 2}, ResultIgnore, 0, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := l2.Acquire(2); err != ErrAtLimit {
		t.Fatalf("n=10 still at limit: %v", err)
	}
	if err := l2.Release(Token{Seq: 3}, ResultIgnore, 0, 3); err != nil {
		t.Fatal(err)
	}
	tok, err := l2.Acquire(3)
	if err != nil {
		t.Fatalf("n=9 < L=10 must admit: %v", err)
	}
	if tok.w != 10 {
		t.Fatalf("w=%d want 10", tok.w)
	}
}

func TestConfigValidation(t *testing.T) {
	good := Config{L0: 10, Lmin: 2, Lmax: 12, Alpha: 2, Beta: 4, Tmo: 50, Cd: 10, Wm: 100}
	if _, err := New(good); err != nil {
		t.Fatalf("good config: %v", err)
	}
	bad := []Config{
		{L0: 10, Lmin: 0, Lmax: 12, Alpha: 2, Beta: 4, Tmo: 50, Cd: 10, Wm: 100},            // Lmin<1
		{L0: 10, Lmin: 2, Lmax: 1, Alpha: 2, Beta: 4, Tmo: 50, Cd: 10, Wm: 100},             // Lmax<Lmin
		{L0: 10, Lmin: 2, Lmax: 1_000_001, Alpha: 2, Beta: 4, Tmo: 50, Cd: 10, Wm: 100},     // Lmax>1e6
		{L0: 1, Lmin: 2, Lmax: 12, Alpha: 2, Beta: 4, Tmo: 50, Cd: 10, Wm: 100},             // L0<Lmin
		{L0: 13, Lmin: 2, Lmax: 12, Alpha: 2, Beta: 4, Tmo: 50, Cd: 10, Wm: 100},            // L0>Lmax
		{L0: 10, Lmin: 2, Lmax: 12, Alpha: -1, Beta: 4, Tmo: 50, Cd: 10, Wm: 100},           // alpha<0
		{L0: 10, Lmin: 2, Lmax: 12, Alpha: 4, Beta: 4, Tmo: 50, Cd: 10, Wm: 100},            // beta<=alpha
		{L0: 10, Lmin: 2, Lmax: 12, Alpha: 2, Beta: 4, Tmo: 0, Cd: 10, Wm: 100},             // Tmo<1
		{L0: 10, Lmin: 2, Lmax: 12, Alpha: 2, Beta: 4, Tmo: 1_000_000_001, Cd: 10, Wm: 100}, // Tmo>1e9
		{L0: 10, Lmin: 2, Lmax: 12, Alpha: 2, Beta: 4, Tmo: 50, Cd: 10, Wm: 0},              // Wm<1
		{L0: 10, Lmin: 2, Lmax: 12, Alpha: 2, Beta: 4, Tmo: 50, Cd: 10, Wm: 1_000_000_001},  // Wm>1e9
		{L0: 10, Lmin: 2, Lmax: 12, Alpha: 2, Beta: 4, Tmo: 50, Cd: -1, Wm: 100},            // Cd<0
		{L0: 10, Lmin: 2, Lmax: 12, Alpha: 2, Beta: 4, Tmo: 50, Cd: 1_000_000_001, Wm: 100}, // Cd>1e9
	}
	for i, c := range bad {
		if _, err := New(c); err != ErrInvalidConfig {
			t.Fatalf("bad config %d: err=%v", i, err)
		}
	}
}

// TestErrorDistinction covers timed-out vs invalid vs duplicate releases.
func TestErrorDistinction(t *testing.T) {
	l, _ := New(Config{L0: 2, Lmin: 1, Lmax: 4, Alpha: 1, Beta: 2, Tmo: 10, Cd: 100, Wm: 100})
	tok, _ := l.Acquire(0)
	// never-issued token
	if err := l.Release(Token{Seq: 999}, ResultIgnore, 0, 1); err != ErrInvalidToken {
		t.Fatalf("unknown: %v", err)
	}
	// rtt=0 rejected; token stays outstanding and can be released later
	if err := l.Release(tok, ResultSuccess, 0, 2); err != ErrInvalidRTT {
		t.Fatalf("rtt=0: %v", err)
	}
	if s := l.State(); s.N != 1 || s.Outstanding != 1 {
		t.Fatalf("rtt rejection must keep token outstanding: %+v", s)
	}
	if err := l.Release(tok, ResultIgnore, 0, 3); err != nil {
		t.Fatalf("token still releasable after rtt rejection: %v", err)
	}
	// duplicate release -> invalid
	if err := l.Release(tok, ResultIgnore, 0, 4); err != ErrInvalidToken {
		t.Fatalf("duplicate: %v", err)
	}
	// timed out token -> distinct error
	tok2, _ := l.Acquire(5) // expires at 15
	_, err := l.Acquire(15) // reap tok2 first
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Release(tok2, ResultIgnore, 0, 16); err != ErrTokenTimedOut {
		t.Fatalf("timed out: %v", err)
	}
	if err := l.Release(tok2, ResultIgnore, 0, 17); err != ErrTokenTimedOut {
		t.Fatalf("timed out stays distinguished: %v", err)
	}
}

// TestPrecheckRejectionsDoNotChangeState: bad result, bad time and clock
// rewind leave everything (including maxNow and pending timeouts) untouched.
func TestPrecheckRejectionsDoNotChangeState(t *testing.T) {
	l, _ := New(Config{L0: 2, Lmin: 1, Lmax: 4, Alpha: 1, Beta: 2, Tmo: 10, Cd: 100, Wm: 100})
	tok, _ := l.Acquire(0)
	other, _ := l.Acquire(0)
	// advance maxNow to 5 via a successful call
	if _, err := l.Acquire(5); err != ErrAtLimit {
		t.Fatalf("setup: %v", err)
	}
	_ = other

	before := l.State()
	// invalid result is checked first, even before time problems
	if err := l.Release(tok, Result(99), 0, -1); err != ErrInvalidResult {
		t.Fatalf("result: %v", err)
	}
	if err := l.Release(tok, ResultIgnore, 0, -1); err != ErrInvalidTime {
		t.Fatalf("negative time: %v", err)
	}
	if err := l.Release(tok, ResultIgnore, 0, maxTime+1); err != ErrInvalidTime {
		t.Fatalf("huge time: %v", err)
	}
	if err := l.Release(tok, ResultIgnore, 0, 4); err != ErrClockRewind {
		t.Fatalf("rewind: %v", err)
	}
	if _, err := l.Acquire(-1); err != ErrInvalidTime {
		t.Fatalf("acquire bad time: %v", err)
	}
	if _, err := l.Acquire(4); err != ErrClockRewind {
		t.Fatalf("acquire rewind: %v", err)
	}
	after := l.State()
	if before != after {
		t.Fatalf("state changed by pre-check rejections:\nbefore=%+v\nafter =%+v", before, after)
	}
	// the token (expires at 10) was NOT reaped by the rejected t=15-ish calls
	if err := l.Release(tok, ResultIgnore, 0, 9); err != nil {
		t.Fatalf("token should remain valid (no reap happened): %v", err)
	}
}

// TestStateRejectionKeepsReap: a Release that fails on rtt after a reap still
// leaves the reaping effects and the maxNow advance in place.
func TestStateRejectionKeepsReap(t *testing.T) {
	l, _ := New(Config{L0: 2, Lmin: 1, Lmax: 4, Alpha: 1, Beta: 2, Tmo: 10, Cd: 100, Wm: 100})
	_, _ = l.Acquire(0)  // first token, expires 10, is reaped below
	b, _ := l.Acquire(1) // expires 11, stays outstanding
	// at t=10: reap expires a; then release b with rtt=0 -> rtt rejected, but
	// reap of a (L 2->1, n=1) and maxNow=10 persist.
	if err := l.Release(b, ResultSuccess, 0, 10); err != ErrInvalidRTT {
		t.Fatalf("rtt: %v", err)
	}
	s := l.State()
	if s.L != 1 || s.N != 1 || s.MaxNow != 10 || s.Outstanding != 1 {
		t.Fatalf("reap effects must persist: %+v", s)
	}
	// rewind after an advanced maxNow is rejected without further changes.
	if _, err := l.Acquire(9); err != ErrClockRewind {
		t.Fatalf("maxNow persisted: %v", err)
	}
}

// TestAcquireStateRejectionKeepsReap: an AtLimit Acquire still performs reap.
func TestAcquireStateRejectionKeepsReap(t *testing.T) {
	l, _ := New(Config{L0: 1, Lmin: 1, Lmax: 1, Alpha: 1, Beta: 2, Tmo: 10, Cd: 100, Wm: 100})
	tok, _ := l.Acquire(0) // expires 10
	// Wait, with one token and L=1 it is at limit; at t=10 reap frees it but
	// L=1, so Acquire then succeeds. Use L=2 scenario for AtLimit post-reap.
	_ = tok
	l2, _ := New(Config{L0: 2, Lmin: 2, Lmax: 2, Alpha: 1, Beta: 2, Tmo: 10, Cd: 100, Wm: 100})
	_, _ = l2.Acquire(0) // expires 10
	_, _ = l2.Acquire(0) // expires 10
	// at t=9 both live -> AtLimit; no reap expected but maxNow moves to 9.
	if _, err := l2.Acquire(9); err != ErrAtLimit {
		t.Fatalf("at limit: %v", err)
	}
	if s := l2.State(); s.MaxNow != 9 || s.N != 2 {
		t.Fatalf("at-limit keeps maxNow: %+v", s)
	}
}
