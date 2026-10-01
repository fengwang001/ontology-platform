package podstate

import (
	"errors"
	"testing"
)

func mustPod(t *testing.T, cfg Config) *Pod {
	t.Helper()
	p, err := New(cfg)
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}
	return p
}

func rejectIs(t *testing.T, err error, want RejectReason) {
	t.Helper()
	var re *RejectError
	if !errors.As(err, &re) {
		t.Fatalf("want reject %v, got %v", want, err)
	}
	if re.Reason != want {
		t.Fatalf("want reject %v, got %v (%v)", want, re.Reason, err)
	}
}

func snap(t *testing.T, p *Pod, c int) ContainerSnapshot {
	t.Helper()
	s, err := p.Container(c)
	if err != nil {
		t.Fatalf("Container(%d): %v", c, err)
	}
	return s
}

func mustStartExit(t *testing.T, p *Pod, c int, startNow int64, code, exitNow int64) {
	t.Helper()
	if err := p.Start(c, startNow); err != nil {
		t.Fatalf("Start(%d,%d): %v", c, startNow, err)
	}
	if err := p.Exit(c, code, exitNow); err != nil {
		t.Fatalf("Exit(%d,%d,%d): %v", c, code, exitNow, err)
	}
}

func baseCfg() Config {
	return Config{
		InitContainers: 1,
		AppContainers:  1,
		RestartPolicy:  Always,
		BackoffBase:    1000,
		BackoffMax:     8000,
		BackoffReset:   5000,
		ActiveDeadline: 10000,
		RestartBudget:  2,
	}
}

func TestExampleFromSpec(t *testing.T) {
	p := mustPod(t, baseCfg())

	if err := p.Start(0, 0); err != nil {
		t.Fatal(err)
	}
	if err := p.Exit(0, 0, 100); err != nil {
		t.Fatal(err)
	}
	if got := snap(t, p, 0).State; got != Succeeded {
		t.Fatalf("init state = %v, want Succeeded", got)
	}

	if err := p.Start(1, 150); err != nil {
		t.Fatal(err)
	}
	if err := p.Exit(1, 1, 300); err != nil {
		t.Fatal(err)
	}
	if s := snap(t, p, 1); s.Next != 1300 || s.Fails != 1 {
		t.Fatalf("after first crash: next=%d k=%d, want 1300/1", s.Next, s.Fails)
	}
	if r := p.Restarts(); r != 1 {
		t.Fatalf("restarts=%d want 1", r)
	}

	if err := p.Start(1, 1300); err != nil {
		t.Fatal(err)
	}
	if err := p.Exit(1, 0, 1400); err != nil {
		t.Fatal(err)
	}
	if s := snap(t, p, 1); s.Next != 3400 || s.Fails != 2 {
		t.Fatalf("after zero-code restart: next=%d k=%d, want 3400/2", s.Next, s.Fails)
	}
	if r := p.Restarts(); r != 2 {
		t.Fatalf("restarts=%d want 2", r)
	}

	if err := p.Start(1, 3400); err != nil {
		t.Fatal(err)
	}
	if err := p.Exit(1, 1, 3500); err != nil {
		t.Fatal(err)
	}
	if got := snap(t, p, 1).State; got != Failed {
		t.Fatalf("state after exhausted budget = %v, want Failed", got)
	}
	if s := snap(t, p, 1); s.Next != 3400 || s.Fails != 2 || p.Restarts() != 2 {
		t.Fatalf("budget-exhausted exit changed k/next/restarts: %+v restarts=%d", s, p.Restarts())
	}
	ph, err := p.Phase(3500)
	if err != nil || ph != PhaseFailed {
		t.Fatalf("phase=%v err=%v, want Failed", ph, err)
	}

	p2 := mustPod(t, baseCfg())
	mustStartExit(t, p2, 0, 0, 0, 100)
	mustStartExit(t, p2, 1, 150, 1, 300)
	if err := p2.Start(1, 1300); err != nil {
		t.Fatal(err)
	}
	if err := p2.Exit(1, 0, 1400); err != nil {
		t.Fatal(err)
	}
	if err := p2.Start(1, 3400); err != nil {
		t.Fatal(err)
	}
	if err := p2.Exit(1, 0, 3500); err != nil {
		t.Fatal(err)
	}
	if got := snap(t, p2, 1).State; got != Succeeded {
		t.Fatalf("zero code after exhausted budget = %v, want Succeeded", got)
	}
	ph, _ = p2.Phase(3500)
	if ph != PhaseSucceeded {
		t.Fatalf("phase=%v, want Succeeded", ph)
	}
}

func TestDeadlineGate(t *testing.T) {
	p := mustPod(t, baseCfg())
	mustStartExit(t, p, 0, 0, 0, 100)
	if err := p.Start(1, 9999); err != nil {
		t.Fatalf("start at 9999 should pass, got %v", err)
	}

	p = mustPod(t, baseCfg())
	mustStartExit(t, p, 0, 0, 0, 100)
	rejectIs(t, p.Start(1, 10000), RejectDeadlineExceeded)

	p = mustPod(t, baseCfg())
	mustStartExit(t, p, 0, 0, 0, 100)
	ph, _ := p.Phase(9999)
	if ph == PhaseFailed {
		t.Fatalf("phase at D-1 = Failed, want non-Failed")
	}
	ph, _ = p.Phase(10000)
	if ph != PhaseFailed {
		t.Fatalf("phase at D = %v, want Failed", ph)
	}

	// After deadline: Start rejected but Exit still accepted.
	p = mustPod(t, baseCfg())
	mustStartExit(t, p, 0, 0, 0, 100)
	if err := p.Start(1, 5000); err != nil {
		t.Fatal(err)
	}
	if err := p.Exit(1, 0, 10001); err != nil {
		t.Fatalf("exit after deadline should succeed, got %v", err)
	}

	// D == 0 means no deadline.
	cfg := baseCfg()
	cfg.ActiveDeadline = 0
	p = mustPod(t, cfg)
	mustStartExit(t, p, 0, 0, 0, 100)
	if err := p.Start(1, 1<<40); err != nil {
		t.Fatalf("D=0 should never gate: %v", err)
	}
}

func TestReasonStrings(t *testing.T) {
	p := mustPod(t, baseCfg())
	mustStartExit(t, p, 0, 0, 0, 100)
	mustStartExit(t, p, 1, 200, 1, 300) // next=1300
	r, err := p.Reason(1, 1299)
	if err != nil || r != "CrashLoopBackOff" {
		t.Fatalf("reason=%q err=%v", r, err)
	}
	r, _ = p.Reason(1, 1300)
	if r != "Waiting" {
		t.Fatalf("reason at next = %q want Waiting", r)
	}
	// Deadline wins over CrashLoopBackOff for a non-terminal container.
	r, _ = p.Reason(1, 10000)
	if r != "DeadlineExceeded" {
		t.Fatalf("reason at deadline = %q want DeadlineExceeded", r)
	}
	// Succeeded terminal container keeps its own reason.
	r, _ = p.Reason(0, 10000)
	if r != "Succeeded" {
		t.Fatalf("succeeded init reason = %q want Succeeded", r)
	}
}

func TestBackoffResetBoundary(t *testing.T) {
	for _, tc := range []struct {
		ran       int64
		wantDelay int64
	}{
		{4999, 2000},
		{5000, 1000},
	} {
		cfg := baseCfg()
		cfg.RestartBudget = 0
		p := mustPod(t, cfg)
		mustStartExit(t, p, 0, 0, 0, 100)
		mustStartExit(t, p, 1, 200, 1, 250) // k=1, next=1250
		start := int64(1250)
		exitAt := start + tc.ran
		mustStartExit(t, p, 1, start, 1, exitAt)
		if got := snap(t, p, 1).Next - exitAt; got != tc.wantDelay {
			t.Fatalf("ran=%d: delay=%d want %d", tc.ran, got, tc.wantDelay)
		}
	}
}

func TestBackoffCapAndOverflow(t *testing.T) {
	if got := backoffDelay(1000, 0, 8000); got != 1000 {
		t.Fatalf("k=0 delay=%d", got)
	}
	if got := backoffDelay(1000, 3, 8000); got != 8000 {
		t.Fatalf("B*2^k==M: got %d want 8000", got)
	}
	if got := backoffDelay(1000, 4, 8000); got != 8000 {
		t.Fatalf("B*2^k>M: got %d want 8000", got)
	}
	if got := backoffDelay(1_000_000_000_000, 1_000_000, 1_000_000_000_000); got != 1_000_000_000_000 {
		t.Fatalf("huge k: got %d", got)
	}
	if got := backoffDelay(1, 100, 1<<60); got != 1<<60 {
		t.Fatalf("near-overflow k: got %d", got)
	}

	cfg := baseCfg()
	cfg.RestartBudget = 0
	cfg.BackoffReset = 1e12
	cfg.ActiveDeadline = 0
	p := mustPod(t, cfg)
	mustStartExit(t, p, 0, 0, 0, 100)
	wantDelays := []int64{1000, 2000, 4000, 8000, 8000, 8000}
	now := int64(200)
	for i, want := range wantDelays {
		mustStartExit(t, p, 1, now, 1, now)
		s := snap(t, p, 1)
		if s.Next-now != want {
			t.Fatalf("crash %d: delay=%d want %d", i, s.Next-now, want)
		}
		now = s.Next
	}
}

func TestNextBoundary(t *testing.T) {
	cfg := baseCfg()
	cfg.RestartBudget = 0
	p := mustPod(t, cfg)
	mustStartExit(t, p, 0, 0, 0, 100)
	mustStartExit(t, p, 1, 200, 1, 300)
	rejectIs(t, p.Start(1, 1299), RejectBackingOff)
	if err := p.Start(1, 1300); err != nil {
		t.Fatalf("start exactly at next should pass: %v", err)
	}
}

func TestPolicies(t *testing.T) {
	// Always + exit 0 on app -> restart with backoff.
	p := mustPod(t, baseCfg())
	mustStartExit(t, p, 0, 0, 0, 100)
	mustStartExit(t, p, 1, 200, 0, 300)
	s := snap(t, p, 1)
	if s.State != Waiting || s.Next != 1300 || s.Fails != 1 {
		t.Fatalf("Always exit0: %+v", s)
	}

	// OnFailure: exit 0 terminal Succeeded; nonzero restarts.
	cfg := baseCfg()
	cfg.RestartPolicy = OnFailure
	p = mustPod(t, cfg)
	mustStartExit(t, p, 0, 0, 0, 100)
	mustStartExit(t, p, 1, 200, 0, 300)
	if got := snap(t, p, 1).State; got != Succeeded {
		t.Fatalf("OnFailure exit0 = %v", got)
	}
	p = mustPod(t, cfg)
	mustStartExit(t, p, 0, 0, 0, 100)
	mustStartExit(t, p, 1, 200, 7, 300)
	if got := snap(t, p, 1).State; got != Waiting {
		t.Fatalf("OnFailure exit7 = %v", got)
	}

	// Never: nonzero app -> Failed, Phase Failed.
	cfg = baseCfg()
	cfg.RestartPolicy = Never
	p = mustPod(t, cfg)
	mustStartExit(t, p, 0, 0, 0, 100)
	mustStartExit(t, p, 1, 200, 1, 300)
	if got := snap(t, p, 1).State; got != Failed {
		t.Fatalf("Never exit1 = %v", got)
	}
	ph, _ := p.Phase(300)
	if ph != PhaseFailed {
		t.Fatalf("phase=%v want Failed", ph)
	}

	// Init with Never, nonzero -> Failed and app gated.
	cfg = baseCfg()
	cfg.RestartPolicy = Never
	p = mustPod(t, cfg)
	if err := p.Start(0, 0); err != nil {
		t.Fatal(err)
	}
	if err := p.Exit(0, 1, 50); err != nil {
		t.Fatal(err)
	}
	if got := snap(t, p, 0).State; got != Failed {
		t.Fatalf("init = %v want Failed", got)
	}
	ph, _ = p.Phase(60)
	if ph != PhaseFailed {
		t.Fatalf("phase=%v want Failed", ph)
	}
	rejectIs(t, p.Start(1, 60), RejectInitNotComplete)

	// Init exit 0 with Never is still Succeeded (success ignores policy).
	p = mustPod(t, cfg)
	mustStartExit(t, p, 0, 0, 0, 10)
	if got := snap(t, p, 0).State; got != Succeeded {
		t.Fatalf("init success under Never = %v", got)
	}
}

func TestBudgetSharedAndUnlimited(t *testing.T) {
	cfg := Config{
		InitContainers: 0,
		AppContainers:  2,
		RestartPolicy:  OnFailure,
		BackoffBase:    10,
		BackoffMax:     100,
		BackoffReset:   1e12,
		ActiveDeadline: 0,
		RestartBudget:  1,
	}
	p := mustPod(t, cfg)
	mustStartExit(t, p, 0, 0, 1, 0) // consumes the single shared budget
	if p.Restarts() != 1 {
		t.Fatalf("restarts=%d want 1", p.Restarts())
	}
	if err := p.Start(0, 10); err != nil {
		t.Fatal(err)
	}
	if err := p.Exit(0, 1, 20); err != nil {
		t.Fatal(err)
	}
	if got := snap(t, p, 0).State; got != Failed {
		t.Fatalf("shared-budget exhaustion c0 = %v", got)
	}
	// Container 1 never used the budget, yet it is exhausted globally.
	if err := p.Start(1, 30); err != nil {
		t.Fatal(err)
	}
	if err := p.Exit(1, 1, 40); err != nil {
		t.Fatal(err)
	}
	if got := snap(t, p, 1).State; got != Failed {
		t.Fatalf("shared-budget exhaustion c1 = %v", got)
	}
	if s := snap(t, p, 0); s.Fails != 1 || s.Next != 10 {
		t.Fatalf("budget-exhausted exits must not touch k/next: %+v", s)
	}

	// X == 0 means unlimited.
	cfg.RestartBudget = 0
	p = mustPod(t, cfg)
	now := int64(0)
	for i := 0; i < 50; i++ {
		if err := p.Start(0, now); err != nil {
			t.Fatalf("iter %d start: %v", i, err)
		}
		if err := p.Exit(0, 1, now); err != nil {
			t.Fatalf("iter %d exit: %v", i, err)
		}
		now = snap(t, p, 0).Next
	}
	if p.Restarts() != 50 {
		t.Fatalf("unlimited restarts=%d want 50", p.Restarts())
	}
}

func TestInitOrderingAndPendingRunning(t *testing.T) {
	cfg := baseCfg()
	cfg.InitContainers = 2
	cfg.AppContainers = 2
	cfg.RestartPolicy = Never
	p := mustPod(t, cfg)

	// Init 1 cannot start before init 0.
	rejectIs(t, p.Start(1, 0), RejectInitNotComplete)
	rejectIs(t, p.Start(2, 0), RejectInitNotComplete)
	ph, _ := p.Phase(0)
	if ph != PhasePending {
		t.Fatalf("fresh phase=%v want Pending", ph)
	}

	mustStartExit(t, p, 0, 0, 0, 10)
	rejectIs(t, p.Start(2, 20), RejectInitNotComplete)
	ph, _ = p.Phase(20)
	if ph != PhasePending {
		t.Fatalf("init incomplete phase=%v want Pending", ph)
	}

	mustStartExit(t, p, 1, 20, 0, 30)
	// Inits done but no app container ever started -> still Pending.
	ph, _ = p.Phase(40)
	if ph != PhasePending {
		t.Fatalf("no app started phase=%v want Pending", ph)
	}

	if err := p.Start(2, 40); err != nil {
		t.Fatal(err)
	}
	ph, _ = p.Phase(50)
	if ph != PhaseRunning {
		t.Fatalf("one app running phase=%v want Running", ph)
	}
	if err := p.Exit(2, 0, 60); err != nil {
		t.Fatal(err)
	}
	// c2 Succeeded (Never), c3 never started: terminal set incomplete, but
	// an app has started -> Running.
	ph, _ = p.Phase(70)
	if ph != PhaseRunning {
		t.Fatalf("one app done one never-started phase=%v want Running", ph)
	}

	// Parallel apps: c3 can start while c2 is terminal.
	if err := p.Start(3, 70); err != nil {
		t.Fatal(err)
	}
	if err := p.Exit(3, 0, 80); err != nil {
		t.Fatal(err)
	}
	ph, _ = p.Phase(90)
	if ph != PhaseSucceeded {
		t.Fatalf("all apps succeeded phase=%v want Succeeded", ph)
	}
}

func TestRejectOrdering(t *testing.T) {
	p := mustPod(t, baseCfg())

	// 1) invalid argument beats everything (even back-then clocks).
	rejectIs(t, p.Start(-1, -1), RejectInvalidArgument)
	rejectIs(t, p.Exit(99, 0, -5), RejectInvalidArgument)

	// 2) clock regression beats state mismatch: establish maxNow first.
	if err := p.Start(0, 100); err != nil {
		t.Fatal(err)
	}
	rejectIs(t, p.Start(0, 50), RejectClockWentBack)
	rejectIs(t, p.Exit(1, 0, 50), RejectClockWentBack)

	// 3) state mismatch for Start vs Exit are distinguishable.
	rejectIs(t, p.Start(0, 100), RejectStartNotWaiting)
	rejectIs(t, p.Exit(1, 0, 100), RejectExitNotRunning)

	// deadline vs init-gate vs backoff ordering on a Waiting app container:
	p = mustPod(t, baseCfg())
	mustStartExit(t, p, 0, 0, 0, 100)
	// at 10000 deadline triggers before init... init is complete here;
	// verify deadline takes precedence over backoff.
	mustStartExit(t, p, 1, 200, 1, 300) // next=1300
	rejectIs(t, p.Start(1, 10000), RejectDeadlineExceeded)

	// init incomplete beats backoff: init 1 waiting with a future next,
	// init 0 not succeeded -> InitNotComplete first.
	cfg := baseCfg()
	cfg.InitContainers = 2
	p = mustPod(t, cfg)
	mustStartExit(t, p, 0, 0, 1, 50) // restarts, next=1050
	// init 1 never started (no backoff); use app instead with init pending:
	rejectIs(t, p.Start(2, 60), RejectInitNotComplete)
	// init 0 itself at 1049 is in backoff.
	rejectIs(t, p.Start(0, 1049), RejectBackingOff)
}

func TestRejectedOpsChangeNothing(t *testing.T) {
	p := mustPod(t, baseCfg())
	mustStartExit(t, p, 0, 0, 0, 100)
	mustStartExit(t, p, 1, 200, 1, 300)

	before := snap(t, p, 1)
	t0v, hasT0 := p.T0()
	rBefore := p.Restarts()

	p.Start(1, 1299)  // backing off
	p.Start(1, 10000) // deadline
	p.Exit(0, 0, 200) // terminal, not Running
	p.Start(5, 400)   // bad index
	p.Start(1, 50)    // clock regression

	after := snap(t, p, 1)
	if after != before {
		t.Fatalf("rejected ops changed container: before=%+v after=%+v", before, after)
	}
	if r := p.Restarts(); r != rBefore {
		t.Fatalf("rejected ops changed restarts: %d -> %d", rBefore, r)
	}
	if v, ok := p.T0(); !ok || v != t0v {
		t.Fatalf("rejected ops changed t0: %d,%v -> %d,%v", t0v, hasT0, v, ok)
	}
	// maxNow unchanged: 300 remains the accepted maximum.
	rejectIs(t, p.Exit(0, 0, 299), RejectClockWentBack)
}

func TestT0AndTerminalImmutability(t *testing.T) {
	p := mustPod(t, baseCfg())
	if _, ok := p.T0(); ok {
		t.Fatal("t0 should be unset initially")
	}
	// A rejected first Start must not set t0.
	rejectIs(t, p.Start(1, 0), RejectInitNotComplete)
	if _, ok := p.T0(); ok {
		t.Fatal("rejected start must not set t0")
	}
	if err := p.Start(0, 0); err != nil {
		t.Fatal(err)
	}
	t0v, ok := p.T0()
	if !ok || t0v != 0 {
		t.Fatalf("t0=%d,%v want 0,true", t0v, ok)
	}
	if err := p.Exit(0, 0, 100); err != nil {
		t.Fatal(err)
	}

	// Terminal containers stay terminal through further ops.
	rejectIs(t, p.Start(0, 200), RejectStartNotWaiting)
	rejectIs(t, p.Exit(0, 0, 200), RejectExitNotRunning)
	if got := snap(t, p, 0).State; got != Succeeded {
		t.Fatalf("terminal changed: %v", got)
	}
}

func TestConfigValidation(t *testing.T) {
	type mut func(*Config)
	cases := []struct {
		name string
		m    mut
	}{
		{"init neg", func(c *Config) { c.InitContainers = -1 }},
		{"init too big", func(c *Config) { c.InitContainers = 17 }},
		{"app zero", func(c *Config) { c.AppContainers = 0 }},
		{"app too big", func(c *Config) { c.AppContainers = 17 }},
		{"bad policy", func(c *Config) { c.RestartPolicy = RestartPolicy(9) }},
		{"B zero", func(c *Config) { c.BackoffBase = 0 }},
		{"B>M", func(c *Config) { c.BackoffBase = 9000 }},
		{"M huge", func(c *Config) { c.BackoffMax = 1e12 + 1 }},
		{"R zero", func(c *Config) { c.BackoffReset = 0 }},
		{"R huge", func(c *Config) { c.BackoffReset = 1e12 + 1 }},
		{"D neg", func(c *Config) { c.ActiveDeadline = -1 }},
		{"D huge", func(c *Config) { c.ActiveDeadline = 1e12 + 1 }},
		{"X neg", func(c *Config) { c.RestartBudget = -1 }},
		{"X huge", func(c *Config) { c.RestartBudget = 1e6 + 1 }},
	}
	for _, tc := range cases {
		c := baseCfg()
		tc.m(&c)
		if _, err := New(c); err == nil {
			t.Fatalf("%s: bad config accepted: %+v", tc.name, c)
		}
	}
}
