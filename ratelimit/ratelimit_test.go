package ratelimit

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func testConfig() Config {
	return Config{
		Window:    time.Minute,
		Threshold: 3,
		BaseLock:  10 * time.Second,
		MaxLock:   time.Minute,
		Cooldown:  5 * time.Minute,
	}
}

func failN(t *testing.T, l *Limiter, account, source string, start time.Time, n int) []Result {
	t.Helper()
	out := make([]Result, n)
	for i := 0; i < n; i++ {
		out[i] = l.Attempt(account, source, false, start.Add(time.Duration(i)*time.Second))
		if out[i].Locked || out[i].Allowed || out[i].Rejected {
			t.Fatalf("failure %d: expected plain wrong-password result, got %+v", i+1, out[i])
		}
	}
	return out
}

// During a lockout even a correct password is denied, and the denied attempt
// neither counts as a failure nor extends the lock.
func TestCorrectPasswordRejectedWhileLocked(t *testing.T) {
	l, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	failN(t, l, "alice", "ip-1", t0, 3)

	res := l.Attempt("alice", "ip-1", true, t0.Add(4*time.Second))
	if !res.Locked || res.Allowed || res.Reason != ReasonLocked {
		t.Fatalf("expected locked denial, got %+v", res)
	}
	wantUnlock := t0.Add(2 * time.Second).Add(10 * time.Second)
	if !res.UnlockAt.Equal(wantUnlock) {
		t.Fatalf("unlock at %s, want %s", res.UnlockAt, wantUnlock)
	}
	if len(res.Dimensions) != 2 {
		t.Fatalf("dimensions %v, want account+source", res.Dimensions)
	}

	// Denied attempts must not extend the lock: right at the deadline the
	// key is unlocked again.
	res = l.Attempt("alice", "ip-1", false, t0.Add(12*time.Second))
	if res.Locked || res.Allowed {
		t.Fatalf("expected fresh failure counting after unlock, got %+v", res)
	}
}

// After a lockout expires, the first failure restarts the count from one; the
// K-th post-expiry failure triggers a new, longer lock.
func TestFailuresReaccumulateAfterLockExpiry(t *testing.T) {
	l, _ := New(testConfig())
	failN(t, l, "alice", "ip-1", t0, 3) // level 1 until t0+12s

	r := l.Attempt("alice", "ip-1", false, t0.Add(12*time.Second))
	if r.Locked {
		t.Fatalf("first post-expiry failure must not be locked: %+v", r)
	}
	r2 := l.Attempt("alice", "ip-1", false, t0.Add(13*time.Second)) // #2
	if r2.Locked {
		t.Fatalf("second post-expiry failure must not lock: %+v", r2)
	}
	// Third post-expiry failure (at t0+14s) triggers level 2: a 20s lock.
	if r := l.Attempt("alice", "ip-1", false, t0.Add(14*time.Second)); r.Locked {
		t.Fatalf("the triggering failure reports wrong password, not locked: %+v", r)
	}
	locked := l.Attempt("alice", "ip-1", true, t0.Add(15*time.Second))
	if !locked.Locked || !locked.UnlockAt.Equal(t0.Add(34*time.Second)) {
		t.Fatalf("level-2 lock should run to %s, got %+v", t0.Add(34*time.Second), locked)
	}
}

// Lockout durations double each level and are capped at MaxLock.
func TestLockDurationDoublesAndCaps(t *testing.T) {
	cfg := testConfig()
	cfg.Threshold = 1
	cfg.BaseLock = 10 * time.Second
	cfg.MaxLock = 25 * time.Second
	l, _ := New(cfg)

	want := []time.Duration{10 * time.Second, 20 * time.Second, 25 * time.Second, 25 * time.Second}
	now := t0
	for level, d := range want {
		r := l.Attempt("alice", "ip-1", false, now)
		if r.Locked {
			t.Fatalf("triggering failure at level %d must not be reported locked", level+1)
		}
		probe := l.Attempt("alice", "ip-1", true, now.Add(time.Second))
		if !probe.Locked || !probe.UnlockAt.Equal(now.Add(d)) {
			t.Fatalf("level %d: unlock at %s, want %s (%+v)", level+1, probe.UnlockAt, now.Add(d), probe)
		}
		now = now.Add(d)
	}
}

// Once the previous lockout expired at least Cooldown ago, the progressive
// level resets and the next lock is a first-level lock again.
func TestCooldownResetsLevel(t *testing.T) {
	l, _ := New(testConfig())
	failN(t, l, "alice", "ip-1", t0, 3) // level 1, expires t0+12s

	start := t0.Add(12 * time.Second).Add(5 * time.Minute)
	failN(t, l, "alice", "ip-1", start, 3)
	probe := l.Attempt("alice", "ip-1", true, start.Add(2*time.Second))
	if !probe.Locked || !probe.UnlockAt.Equal(start.Add(2*time.Second).Add(10*time.Second)) {
		t.Fatalf("cooldown should reset level to 1, got %+v", probe)
	}
}

// A successful login clears the account key only; source-side failures
// survive and can still trip the source lock.
func TestSuccessClearsAccountKeyOnly(t *testing.T) {
	l, _ := New(testConfig())
	failN(t, l, "alice", "ip-1", t0, 2)
	if r := l.Attempt("alice", "ip-1", true, t0.Add(2*time.Second)); !r.Allowed {
		t.Fatalf("correct password should be allowed: %+v", r)
	}

	// Source ip-1 still carries the two earlier failures. One failure from
	// bob is the source's third -> source locks, while bob's account (one
	// failure only) and alice's account stay unlocked.
	if r := l.Attempt("bob", "ip-1", false, t0.Add(3*time.Second)); r.Locked {
		t.Fatalf("the triggering failure reports a wrong password: %+v", r)
	}
	probe := l.Attempt("bob", "ip-1", true, t0.Add(4*time.Second))
	if !probe.Locked {
		t.Fatalf("source should be locked: %+v", probe)
	}
	if len(probe.Dimensions) != 1 || probe.Dimensions[0] != DimensionSource {
		t.Fatalf("only the source dimension must be locked, got %v", probe.Dimensions)
	}
	if e := l.accounts["bob"]; e != nil && t0.Add(4*time.Second).Before(e.lockedUntil) {
		t.Fatalf("bob's account must not be locked")
	}
	if e := l.accounts["alice"]; e.level != 0 || len(e.failures) != 0 {
		t.Fatalf("alice's account should be reset after success, got level=%d failures=%d", e.level, len(e.failures))
	}
}

// Spraying one password across many accounts from the same source triggers
// the source key while individual accounts stay unlocked.
func TestSourceSprayTriggersSourceLock(t *testing.T) {
	l, _ := New(testConfig())
	for i := 0; i < 3; i++ {
		r := l.Attempt(fmt.Sprintf("user-%d", i), "evil-ip", false, t0.Add(time.Duration(i)*time.Second))
		if r.Locked || r.Allowed {
			t.Fatalf("spray attempt %d: unexpected %+v", i, r)
		}
	}
	probe := l.Attempt("user-9", "evil-ip", true, t0.Add(3*time.Second))
	if !probe.Locked || probe.UnlockAt.IsZero() {
		t.Fatalf("source should be locked, got %+v", probe)
	}
	if len(probe.Dimensions) != 1 || probe.Dimensions[0] != DimensionSource {
		t.Fatalf("only source dimension must be locked, got %v", probe.Dimensions)
	}
	for _, name := range []string{"user-0", "user-1", "user-2"} {
		if e := l.accounts[name]; e != nil && !e.lockedUntil.IsZero() {
			t.Fatalf("account %s must not be locked by a spray", name)
		}
	}
}

// Only timestamps strictly newer than now-W count in the window.
func TestSlidingWindowPruning(t *testing.T) {
	l, _ := New(testConfig()) // W=1m, K=3
	l.Attempt("alice", "ip-1", false, t0)
	l.Attempt("alice", "ip-1", false, t0.Add(30*time.Second))
	l.Attempt("alice", "ip-1", false, t0.Add(60*time.Second))
	r := l.Attempt("alice", "ip-1", false, t0.Add(61*time.Second)) // third in window
	if r.Locked {
		t.Fatalf("third in-window failure triggers but reports wrong password: %+v", r)
	}
	if probe := l.Attempt("alice", "ip-1", true, t0.Add(62*time.Second)); !probe.Locked {
		t.Fatalf("lock should be active after third in-window failure, got %+v", probe)
	}
}

func TestInvalidConfig(t *testing.T) {
	good := testConfig()
	bad := []Config{
		{Window: 0, Threshold: good.Threshold, BaseLock: good.BaseLock, MaxLock: good.MaxLock, Cooldown: good.Cooldown},
		{Window: good.Window, Threshold: 0, BaseLock: good.BaseLock, MaxLock: good.MaxLock, Cooldown: good.Cooldown},
		{Window: good.Window, Threshold: good.Threshold, BaseLock: -time.Second, MaxLock: good.MaxLock, Cooldown: good.Cooldown},
		{Window: good.Window, Threshold: good.Threshold, BaseLock: good.Cooldown, MaxLock: 0, Cooldown: good.Cooldown},
		{Window: good.Window, Threshold: good.Threshold, BaseLock: 10 * time.Second, MaxLock: 5 * time.Second, Cooldown: good.Cooldown},
	}
	for i, cfg := range bad {
		if _, err := New(cfg); err == nil {
			t.Fatalf("case %d: expected construction error", i)
		}
	}
	if _, err := New(good); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestValidationOrderAndNoStateChange(t *testing.T) {
	l, _ := New(testConfig())
	if r := l.Attempt("", "ip-1", false, t0); r.Reason != ReasonEmptyAccount || !r.Rejected {
		t.Fatalf("got %+v", r)
	}
	if r := l.Attempt("alice", "", false, t0); r.Reason != ReasonEmptySource {
		t.Fatalf("got %+v", r)
	}
	l.Attempt("alice", "ip-1", false, t0)
	if r := l.Attempt("alice", "ip-1", false, t0.Add(-time.Second)); r.Reason != ReasonClockRewound {
		t.Fatalf("got %+v", r)
	}
	// Rejected attempts changed nothing: one recorded failure plus two more
	// reaches exactly the threshold.
	failN(t, l, "alice", "ip-1", t0.Add(time.Second), 2)
	if probe := l.Attempt("alice", "ip-1", true, t0.Add(3*time.Second)); !probe.Locked {
		t.Fatalf("expected lock after one recorded + two failures, got %+v", probe)
	}
}

// Concurrent failures are never lost; the K-th failure drives exactly one
// level increment.
func TestConcurrentFailuresNotLost(t *testing.T) {
	const n = 100
	cfg := testConfig()
	cfg.Threshold = n
	l, _ := New(cfg)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := l.Attempt("alice", "ip-1", false, t0)
			if r.Locked || r.Allowed || r.Rejected {
				t.Errorf("unexpected result %+v", r)
			}
		}()
	}
	wg.Wait()

	probe := l.Attempt("alice", "ip-1", true, t0.Add(time.Second))
	if !probe.Locked {
		t.Fatalf("exactly %d concurrent failures must trigger the lock", n)
	}
	if e := l.accounts["alice"]; e.level != 1 || len(e.failures) != 0 {
		t.Fatalf("account level=%d failures=%d, want level 1 with cleared failures", e.level, len(e.failures))
	}
	if e := l.sources["ip-1"]; e.level != 1 {
		t.Fatalf("source level=%d, want 1", e.level)
	}
}

// While a lock is active, concurrent attempts are all denied and none
// mutates state (the deadline stays fixed).
func TestConcurrentAttemptsDeniedWhileLocked(t *testing.T) {
	l, _ := New(testConfig())
	failN(t, l, "alice", "ip-1", t0, 3)

	const n = 64
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := l.Attempt("alice", "ip-1", i%2 == 0, t0.Add(5*time.Second))
			if !r.Locked || r.Allowed || r.Reason != ReasonLocked {
				t.Errorf("attempt %d: %+v", i, r)
			}
		}(i)
	}
	wg.Wait()

	want := t0.Add(12 * time.Second)
	for name, m := range map[string]map[string]*entry{
		"account": l.accounts,
		"source":  l.sources,
	} {
		e := m["alice"]
		if name == "source" {
			e = m["ip-1"]
		}
		if !e.lockedUntil.Equal(want) || e.level != 1 {
			t.Fatalf("%s state mutated during lock: deadline %s level %d", name, e.lockedUntil, e.level)
		}
	}
}

// The same input/clock sequence always yields the same results.
func TestDeterministicReplay(t *testing.T) {
	run := func() []bool {
		l, _ := New(testConfig())
		var allowed []bool
		for i := 0; i < 8; i++ {
			now := t0.Add(time.Duration(i) * 6 * time.Second)
			r := l.Attempt("alice", "ip-1", i == 5, now)
			allowed = append(allowed, r.Allowed, r.Locked)
		}
		return allowed
	}
	first := run()
	for i := 0; i < 3; i++ {
		if got := run(); !equalBools(got, first) {
			t.Fatalf("replay %d differs: %v vs %v", i+1, got, first)
		}
	}
}

func equalBools(a, b []bool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Logs contain the input, output and the decision basis.
func TestAttemptLogging(t *testing.T) {
	l, _ := New(testConfig())
	var rec recordingLogger
	l.SetLogger(&rec)

	l.Attempt("alice", "ip-1", false, t0)
	l.Attempt("alice", "ip-1", false, t0.Add(time.Second))
	l.Attempt("alice", "ip-1", false, t0.Add(2*time.Second))
	l.Attempt("alice", "ip-1", true, t0.Add(3*time.Second))

	lines := rec.lines()
	if len(lines) != 4 {
		t.Fatalf("want 4 log lines, got %d: %v", len(lines), lines)
	}
	for i, line := range lines {
		for _, want := range []string{"alice", "ip-1", "input=", "output=", "basis="} {
			if !strings.Contains(line, want) {
				t.Fatalf("line %d missing %q: %s", i, want, line)
			}
		}
	}
	if !strings.Contains(lines[3], "locked=true") || !strings.Contains(lines[3], "account") ||
		!strings.Contains(lines[3], "source") {
		t.Fatalf("locked log line should expose dimensions, got: %s", lines[3])
	}
}

type recordingLogger struct {
	mu    sync.Mutex
	store []string
}

func (r *recordingLogger) LogAttempt(account, source string, passwordCorrect bool, now time.Time, res Result, basis string) {
	dims := make([]string, len(res.Dimensions))
	for i, d := range res.Dimensions {
		dims[i] = string(d)
	}
	line := fmt.Sprintf("input={account=%q source=%q password_correct=%t now=%s} output={allowed=%t locked=%t reason=%q dimensions=%s} basis=%s",
		account, source, passwordCorrect, now.Format(time.RFC3339Nano),
		res.Allowed, res.Locked, res.Reason, strings.Join(dims, ","), basis)
	r.mu.Lock()
	r.store = append(r.store, line)
	r.mu.Unlock()
}

func (r *recordingLogger) lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.store))
	copy(out, r.store)
	return out
}
