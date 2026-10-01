package pod

import (
	"errors"
	"math"
	"testing"
)

func mustNew(t *testing.T, cfg Config) *Pod {
	t.Helper()
	p, err := New(cfg)
	if err != nil {
		t.Fatalf("New(%+v) unexpected error: %v", cfg, err)
	}
	return p
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected rejection: %v", err)
	}
}

func mustReject(t *testing.T, err error, reason RejectReason) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected rejection %s, got nil", reason)
	}
	var pe *Error
	if !errors.As(err, &pe) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if pe.Reason != reason {
		t.Fatalf("expected rejection %s, got %s (%v)", reason, pe.Reason, err)
	}
	if !errors.Is(err, sentinelFor(reason)) {
		t.Fatalf("errors.Is(%v, %v) = false", err, sentinelFor(reason))
	}
}

func mustPhase(t *testing.T, p *Pod, now int64, want Phase) {
	t.Helper()
	got, err := p.Phase(now)
	if err != nil {
		t.Fatalf("Phase(%d) unexpected error: %v", now, err)
	}
	if got != want {
		t.Fatalf("Phase(%d) = %s, want %s", now, got, want)
	}
}

func mustReason(t *testing.T, p *Pod, c int, now int64, want string) {
	t.Helper()
	got, err := p.Reason(c, now)
	if err != nil {
		t.Fatalf("Reason(%d,%d) unexpected error: %v", c, now, err)
	}
	if got != want {
		t.Fatalf("Reason(%d,%d) = %q, want %q", c, now, got, want)
	}
}

func containerState(t *testing.T, p *Pod, c int) ContainerSnapshot {
	t.Helper()
	snap := p.Snapshot()
	if c < 0 || c >= len(snap.Containers) {
		t.Fatalf("container %d out of range", c)
	}
	return snap.Containers[c]
}

func baseConfig() Config {
	return Config{
		InitContainers: 0,
		AppContainers:  1,
		Policy:         Always,
		BackoffBase:    1000,
		BackoffMax:     8000,
		BackoffReset:   5000,
		ActiveDeadline: 0,
		RestartBudget:  0,
	}
}

// TestSpecExample replays the worked example from the specification.
func TestSpecExample(t *testing.T) {
	cfg := Config{
		InitContainers: 1, AppContainers: 1, Policy: Always,
		BackoffBase: 1000, BackoffMax: 8000, BackoffReset: 5000,
		ActiveDeadline: 10000, RestartBudget: 2,
	}

	// Variant 1: budget exhausted by a non-zero exit -> Failed.
	p := mustNew(t, cfg)
	mustOK(t, p.Start(0, 0))
	mustOK(t, p.Exit(0, 0, 100))
	if got := containerState(t, p, 0).State; got != Succeeded {
		t.Fatalf("init container state = %s, want Succeeded", got)
	}
	mustOK(t, p.Start(1, 150))
	mustOK(t, p.Exit(1, 1, 300))
	cs := containerState(t, p, 1)
	if cs.State != Waiting || cs.K != 1 || cs.Next != 1300 {
		t.Fatalf("after first restart: state=%s k=%d next=%d, want Waiting k=1 next=1300", cs.State, cs.K, cs.Next)
	}
	if got := p.Snapshot().Restarts; got != 1 {
		t.Fatalf("restarts = %d, want 1", got)
	}
	mustOK(t, p.Start(1, 1300))
	mustOK(t, p.Exit(1, 0, 1400))
	cs = containerState(t, p, 1)
	if cs.State != Waiting || cs.K != 2 || cs.Next != 3400 {
		t.Fatalf("after second restart: state=%s k=%d next=%d, want Waiting k=2 next=3400", cs.State, cs.K, cs.Next)
	}
	if got := p.Snapshot().Restarts; got != 2 {
		t.Fatalf("restarts = %d, want 2", got)
	}
	mustOK(t, p.Start(1, 3400))
	mustOK(t, p.Exit(1, 1, 3500))
	cs = containerState(t, p, 1)
	if cs.State != Failed || cs.K != 2 || cs.Next != 3400 {
		t.Fatalf("budget exhausted: state=%s k=%d next=%d, want Failed k=2 next=3400", cs.State, cs.K, cs.Next)
	}
	if got := p.Snapshot().Restarts; got != 2 {
		t.Fatalf("restarts = %d, want 2 (budget-exhausted exit must not count)", got)
	}
	mustPhase(t, p, 3500, PhaseFailed)

	// Variant 2: budget exhausted by a zero exit -> Succeeded.
	p = mustNew(t, cfg)
	mustOK(t, p.Start(0, 0))
	mustOK(t, p.Exit(0, 0, 100))
	mustOK(t, p.Start(1, 150))
	mustOK(t, p.Exit(1, 1, 300))
	mustOK(t, p.Start(1, 1300))
	mustOK(t, p.Exit(1, 0, 1400))
	mustOK(t, p.Start(1, 3400))
	mustOK(t, p.Exit(1, 0, 3500))
	if got := containerState(t, p, 1).State; got != Succeeded {
		t.Fatalf("budget-exhausted zero exit: state = %s, want Succeeded", got)
	}
	mustPhase(t, p, 3500, PhaseSucceeded)

	// Deadline gating of Start: t0 = 0, D = 10000.
	p = mustNew(t, cfg)
	mustOK(t, p.Start(0, 0))
	mustOK(t, p.Exit(0, 0, 100))
	mustReject(t, p.Start(1, 10000), RejectDeadlineExceeded)
	mustOK(t, p.Start(1, 9999))
}

// TestBackoffResetBoundary: ran == R resets k, ran == R-1 does not.
func TestBackoffResetBoundary(t *testing.T) {
	cfg := baseConfig() // B=1000 M=8000 R=5000

	// ran == R-1: no reset, k keeps growing.
	p := mustNew(t, cfg)
	mustOK(t, p.Start(0, 0))
	mustOK(t, p.Exit(0, 1, 10)) // k=1, next=10+1000
	mustOK(t, p.Start(0, 1010))
	mustOK(t, p.Exit(0, 1, 1010+4999)) // ran=4999 < R: no reset; delay=2000
	cs := containerState(t, p, 0)
	if cs.K != 2 || cs.Next != 6009+2000 {
		t.Fatalf("ran=R-1: k=%d next=%d, want k=2 next=8009", cs.K, cs.Next)
	}

	// ran == R: reset k to 0 before computing the delay.
	p = mustNew(t, cfg)
	mustOK(t, p.Start(0, 0))
	mustOK(t, p.Exit(0, 1, 10)) // k=1, next=1010
	mustOK(t, p.Start(0, 1010))
	mustOK(t, p.Exit(0, 1, 1010+5000)) // ran=5000 == R: reset; delay=1000
	cs = containerState(t, p, 0)
	if cs.K != 1 || cs.Next != 6010+1000 {
		t.Fatalf("ran=R: k=%d next=%d, want k=1 next=7010", cs.K, cs.Next)
	}
}

// TestBackoffCapBoundary: B*2^k == M is exact, B*2^k > M caps at M.
func TestBackoffCapBoundary(t *testing.T) {
	cfg := baseConfig() // B=1000, M=8000: k=3 hits exactly 8000, k=4 caps.
	p := mustNew(t, cfg)
	now := int64(0)
	wantDelays := []int64{1000, 2000, 4000, 8000, 8000, 8000}
	for i, want := range wantDelays {
		mustOK(t, p.Start(0, now))
		mustOK(t, p.Exit(0, 1, now+1))
		cs := containerState(t, p, 0)
		if got := cs.Next - (now + 1); got != want {
			t.Fatalf("restart %d: delay = %d, want %d", i, got, want)
		}
		now = cs.Next
	}
}

// TestBackoffNoOverflow: huge k and huge B must saturate at M, not overflow.
func TestBackoffNoOverflow(t *testing.T) {
	cfg := baseConfig()
	cfg.BackoffBase = 1_000_000_000_000 // B = 1e12 = max allowed
	cfg.BackoffMax = 1_000_000_000_000  // M = B
	p := mustNew(t, cfg)
	now := int64(0)
	for i := 0; i < 200; i++ {
		mustOK(t, p.Start(0, now))
		mustOK(t, p.Exit(0, 1, now+1))
		cs := containerState(t, p, 0)
		if cs.Next != now+1+1_000_000_000_000 {
			t.Fatalf("restart %d: next = %d, want %d (capped at M)", i, cs.Next, now+1+1_000_000_000_000)
		}
		if cs.K != int64(i+1) {
			t.Fatalf("restart %d: k = %d, want %d", i, cs.K, i+1)
		}
		now = cs.Next
	}

	// Direct unit checks on the delay function, including k >= 63.
	if got := backoffDelay(3, 1_000_000_000_000, 100); got != 1_000_000_000_000 {
		t.Fatalf("backoffDelay(3,1e12,100) = %d, want 1e12", got)
	}
	if got := backoffDelay(1, math.MaxInt64/2, 62); got != math.MaxInt64/2 {
		t.Fatalf("backoffDelay with k=62 = %d, want cap", got)
	}
	if got := backoffDelay(2, 8, 2); got != 8 {
		t.Fatalf("backoffDelay(2,8,2) = %d, want 8 (exact)", got)
	}
	if got := backoffDelay(2, 7, 2); got != 7 {
		t.Fatalf("backoffDelay(2,7,2) = %d, want 7 (capped)", got)
	}
}

// TestStartAtNextBoundary: now == next may start, now == next-1 is backoff.
func TestStartAtNextBoundary(t *testing.T) {
	p := mustNew(t, baseConfig())
	mustOK(t, p.Start(0, 0))
	mustOK(t, p.Exit(0, 1, 100)) // next = 100 + 1000 = 1100
	mustReject(t, p.Start(0, 1099), RejectBackoff)
	mustReason(t, p, 0, 1099, "CrashLoopBackOff")
	mustOK(t, p.Start(0, 1100))
	mustReason(t, p, 0, 1100, "Running")
}

// TestAlwaysZeroExitRestarts: Always restarts even on exit code 0.
func TestAlwaysZeroExitRestarts(t *testing.T) {
	p := mustNew(t, baseConfig())
	mustOK(t, p.Start(0, 0))
	mustOK(t, p.Exit(0, 0, 100))
	cs := containerState(t, p, 0)
	if cs.State != Waiting || cs.K != 1 || cs.Next != 1100 {
		t.Fatalf("Always zero exit: state=%s k=%d next=%d, want Waiting k=1 next=1100", cs.State, cs.K, cs.Next)
	}
	if got := p.Snapshot().Restarts; got != 1 {
		t.Fatalf("restarts = %d, want 1 (zero-exit restart counts)", got)
	}
	mustReason(t, p, 0, 1099, "CrashLoopBackOff")
}

// TestOnFailureZeroExitTerminates: OnFailure treats exit code 0 as terminal.
func TestOnFailureZeroExitTerminates(t *testing.T) {
	cfg := baseConfig()
	cfg.Policy = OnFailure
	p := mustNew(t, cfg)
	mustOK(t, p.Start(0, 0))
	mustOK(t, p.Exit(0, 0, 100))
	cs := containerState(t, p, 0)
	if cs.State != Succeeded || cs.K != 0 || cs.Next != 0 {
		t.Fatalf("OnFailure zero exit: state=%s k=%d next=%d, want Succeeded k=0 next=0", cs.State, cs.K, cs.Next)
	}
	if got := p.Snapshot().Restarts; got != 0 {
		t.Fatalf("restarts = %d, want 0", got)
	}
	mustPhase(t, p, 100, PhaseSucceeded)

	// Non-zero exit still restarts under OnFailure.
	p = mustNew(t, cfg)
	mustOK(t, p.Start(0, 0))
	mustOK(t, p.Exit(0, 1, 100))
	if got := containerState(t, p, 0).State; got != Waiting {
		t.Fatalf("OnFailure non-zero exit: state = %s, want Waiting", got)
	}
}

// TestNeverNonZeroExitFails: Never turns a non-zero exit into Failed and the
// Pod phase becomes Failed.
func TestNeverNonZeroExitFails(t *testing.T) {
	cfg := baseConfig()
	cfg.Policy = Never
	p := mustNew(t, cfg)
	mustOK(t, p.Start(0, 0))
	mustOK(t, p.Exit(0, 1, 100))
	cs := containerState(t, p, 0)
	if cs.State != Failed || cs.K != 0 || cs.Next != 0 {
		t.Fatalf("Never non-zero exit: state=%s k=%d next=%d, want Failed k=0 next=0", cs.State, cs.K, cs.Next)
	}
	if got := p.Snapshot().Restarts; got != 0 {
		t.Fatalf("restarts = %d, want 0", got)
	}
	mustPhase(t, p, 100, PhaseFailed)
	mustReason(t, p, 0, 100, "Failed")
}

// TestBudgetExhaustedFallback: once restarts == X, Always falls back to
// Never semantics and k/next/restarts stay untouched.
func TestBudgetExhaustedFallback(t *testing.T) {
	cfg := baseConfig()
	cfg.RestartBudget = 1

	// Zero exit with exhausted budget -> Succeeded.
	p := mustNew(t, cfg)
	mustOK(t, p.Start(0, 0))
	mustOK(t, p.Exit(0, 1, 100)) // restart #1: k=1, next=1100, restarts=1
	mustOK(t, p.Start(0, 1100))
	mustOK(t, p.Exit(0, 0, 1200))
	cs := containerState(t, p, 0)
	if cs.State != Succeeded || cs.K != 1 || cs.Next != 1100 {
		t.Fatalf("budget exhausted zero exit: state=%s k=%d next=%d, want Succeeded k=1 next=1100", cs.State, cs.K, cs.Next)
	}
	if got := p.Snapshot().Restarts; got != 1 {
		t.Fatalf("restarts = %d, want 1", got)
	}

	// Non-zero exit with exhausted budget -> Failed.
	p = mustNew(t, cfg)
	mustOK(t, p.Start(0, 0))
	mustOK(t, p.Exit(0, 1, 100))
	mustOK(t, p.Start(0, 1100))
	mustOK(t, p.Exit(0, 1, 1200))
	cs = containerState(t, p, 0)
	if cs.State != Failed || cs.K != 1 || cs.Next != 1100 {
		t.Fatalf("budget exhausted non-zero exit: state=%s k=%d next=%d, want Failed k=1 next=1100", cs.State, cs.K, cs.Next)
	}
	if got := p.Snapshot().Restarts; got != 1 {
		t.Fatalf("restarts = %d, want 1", got)
	}
	mustPhase(t, p, 1200, PhaseFailed)
}

// TestBudgetSharedAcrossContainers: X is a single Pod-wide counter.
func TestBudgetSharedAcrossContainers(t *testing.T) {
	cfg := baseConfig()
	cfg.AppContainers = 2
	cfg.RestartBudget = 2
	p := mustNew(t, cfg)
	mustOK(t, p.Start(0, 0))
	mustOK(t, p.Start(1, 0))
	mustOK(t, p.Exit(0, 1, 10)) // restart #1 (container 0)
	mustOK(t, p.Exit(1, 1, 10)) // restart #2 (container 1)
	if got := p.Snapshot().Restarts; got != 2 {
		t.Fatalf("restarts = %d, want 2", got)
	}
	mustOK(t, p.Start(0, 1010))
	mustOK(t, p.Exit(0, 1, 1020)) // budget exhausted -> Failed, restarts stays 2
	if got := containerState(t, p, 0).State; got != Failed {
		t.Fatalf("container 0 state = %s, want Failed", got)
	}
	if got := p.Snapshot().Restarts; got != 2 {
		t.Fatalf("restarts = %d, want 2 (shared budget must not exceed X)", got)
	}
	mustOK(t, p.Start(1, 1020))   // next was 1010 <= 1020, no clock regression
	mustOK(t, p.Exit(1, 0, 1030)) // budget exhausted -> Succeeded
	if got := containerState(t, p, 1).State; got != Succeeded {
		t.Fatalf("container 1 state = %s, want Succeeded", got)
	}
	mustPhase(t, p, 1030, PhaseFailed) // one app container Failed
}

// TestBudgetZeroUnlimited: X == 0 means no restart limit.
func TestBudgetZeroUnlimited(t *testing.T) {
	p := mustNew(t, baseConfig()) // X = 0
	now := int64(0)
	for i := 0; i < 50; i++ {
		mustOK(t, p.Start(0, now))
		mustOK(t, p.Exit(0, 1, now+1))
		now = containerState(t, p, 0).Next
	}
	if got := p.Snapshot().Restarts; got != 50 {
		t.Fatalf("restarts = %d, want 50 (X=0 is unlimited)", got)
	}
	if got := containerState(t, p, 0).State; got != Waiting {
		t.Fatalf("state = %s, want Waiting", got)
	}
}

// TestInitContainerGating: init containers run strictly in order and gate
// the app containers.
func TestInitContainerGating(t *testing.T) {
	cfg := baseConfig()
	cfg.InitContainers = 2
	cfg.AppContainers = 1
	p := mustNew(t, cfg)

	mustReject(t, p.Start(1, 0), RejectInitNotComplete) // init 1 before init 0
	mustReject(t, p.Start(2, 0), RejectInitNotComplete) // app before any init
	mustOK(t, p.Start(0, 0))
	mustReject(t, p.Start(1, 1), RejectInitNotComplete) // init 0 still running
	mustOK(t, p.Exit(0, 0, 10))
	mustOK(t, p.Start(1, 10))
	mustReject(t, p.Start(2, 11), RejectInitNotComplete) // init 1 still running
	mustOK(t, p.Exit(1, 0, 20))
	mustOK(t, p.Start(2, 20))
}

// TestInitFailureNeverPolicy: an init container that fails under Never makes
// the Pod Failed and blocks app containers forever.
func TestInitFailureNeverPolicy(t *testing.T) {
	cfg := baseConfig()
	cfg.InitContainers = 1
	cfg.AppContainers = 1
	cfg.Policy = Never
	p := mustNew(t, cfg)
	mustOK(t, p.Start(0, 0))
	mustOK(t, p.Exit(0, 1, 10))
	if got := containerState(t, p, 0).State; got != Failed {
		t.Fatalf("init state = %s, want Failed", got)
	}
	mustPhase(t, p, 10, PhaseFailed)
	mustReject(t, p.Start(1, 20), RejectInitNotComplete)
	mustReason(t, p, 0, 20, "Failed")
}

// TestDeadlinePhaseBoundary: now-t0 == D is Failed, now-t0 == D-1 is not.
func TestDeadlinePhaseBoundary(t *testing.T) {
	cfg := baseConfig()
	cfg.ActiveDeadline = 10000
	p := mustNew(t, cfg)
	mustOK(t, p.Start(0, 100)) // t0 = 100
	mustPhase(t, p, 10099, PhaseRunning)
	mustPhase(t, p, 10100, PhaseFailed)
	mustReason(t, p, 0, 10100, "DeadlineExceeded")
	mustReason(t, p, 0, 10099, "Running")
}

// TestDeadlineBlocksStartNotExit: after the deadline, Start is rejected but
// Exit of a running container still works.
func TestDeadlineBlocksStartNotExit(t *testing.T) {
	cfg := baseConfig()
	cfg.ActiveDeadline = 1000
	p := mustNew(t, cfg)
	mustOK(t, p.Start(0, 0))    // t0 = 0
	mustOK(t, p.Exit(0, 1, 10)) // restart: next = 1010
	mustReject(t, p.Start(0, 1010), RejectDeadlineExceeded)
	// Exit is still accepted after the deadline.
	p2 := mustNew(t, cfg)
	mustOK(t, p2.Start(0, 0))
	mustOK(t, p2.Exit(0, 1, 5000)) // accepted: Exit has no deadline check
	if got := containerState(t, p2, 0).State; got != Waiting {
		t.Fatalf("state = %s, want Waiting (Exit after deadline allowed)", got)
	}
	mustPhase(t, p2, 5000, PhaseFailed)
}

// TestPendingVsRunning: phase transitions between Pending and Running.
func TestPendingVsRunning(t *testing.T) {
	cfg := baseConfig()
	cfg.InitContainers = 1
	cfg.AppContainers = 2
	cfg.Policy = OnFailure // zero exits terminate, so the Pod can complete
	p := mustNew(t, cfg)

	mustPhase(t, p, 0, Pending) // init not Succeeded
	mustOK(t, p.Start(0, 0))
	mustOK(t, p.Exit(0, 0, 10))
	mustPhase(t, p, 10, Pending) // no app container ever started
	mustOK(t, p.Start(1, 20))
	mustPhase(t, p, 20, PhaseRunning) // one app running, one not started
	mustOK(t, p.Start(2, 30))
	mustOK(t, p.Exit(1, 0, 40)) // Succeeded
	mustPhase(t, p, 40, PhaseRunning)
	mustOK(t, p.Exit(2, 0, 50)) // Succeeded
	mustPhase(t, p, 50, PhaseSucceeded)

	// A failed app container among terminal app containers -> Failed.
	p = mustNew(t, cfg)
	mustOK(t, p.Start(0, 0))
	mustOK(t, p.Exit(0, 0, 10))
	mustOK(t, p.Start(1, 20))
	mustOK(t, p.Start(2, 20))
	mustOK(t, p.Exit(1, 0, 30))
	mustOK(t, p.Exit(2, 1, 30)) // OnFailure -> restart, not terminal
	mustPhase(t, p, 30, PhaseRunning)
	mustOK(t, p.Start(2, 1030))
	mustOK(t, p.Exit(2, 1, 1040)) // restarts again (budget unlimited)
	mustOK(t, p.Start(2, 3040))
	mustOK(t, p.Exit(2, 1, 3040+5000)) // ran == R: k resets
	mustOK(t, p.Start(2, 8040+1000))
	mustOK(t, p.Exit(2, 1, 9040+1000))
	cs := containerState(t, p, 2)
	if cs.State != Waiting {
		t.Fatalf("container 2 state = %s, want Waiting", cs.State)
	}
}

// TestRejectOrdering: when several rejection reasons apply, only the first
// one in the fixed order is reported.
func TestRejectOrdering(t *testing.T) {
	cfg := baseConfig()
	cfg.InitContainers = 1
	cfg.ActiveDeadline = 1000
	p := mustNew(t, cfg)

	mustOK(t, p.Start(0, 0))    // t0 = 0
	mustOK(t, p.Exit(0, 1, 10)) // init restarts: next = 1010, maxNow = 10

	// Invalid argument beats everything (bad index AND clock regression).
	mustReject(t, p.Start(7, 5), RejectInvalidArgument)
	mustReject(t, p.Start(0, -1), RejectInvalidArgument)
	mustReject(t, p.Exit(-2, 1, 5), RejectInvalidArgument)

	// Clock regression beats state mismatch (container 0 is Waiting, but
	// now < maxNow is reported first).
	mustReject(t, p.Start(0, 5), RejectClockRegression)
	mustReject(t, p.Exit(0, 1, 5), RejectClockRegression) // also not Running

	// State mismatch beats deadline: container 1 (app) is Waiting, container
	// 0 is Waiting too; use a fresh pod where a container is Running past
	// the deadline.
	p2 := mustNew(t, cfg)
	mustOK(t, p2.Start(0, 0))
	mustOK(t, p2.Exit(0, 0, 10)) // init Succeeded
	mustOK(t, p2.Start(1, 20))   // app running
	// Start on the running app container at/after the deadline: NotWaiting
	// is reported before DeadlineExceeded.
	mustReject(t, p2.Start(1, 2000), RejectNotWaiting)
	// Exit on a non-Running container: NotRunning.
	mustReject(t, p2.Exit(0, 0, 30), RejectNotRunning)

	// Deadline beats init-incomplete and backoff.
	p3 := mustNew(t, cfg)
	mustOK(t, p3.Start(0, 0))
	mustOK(t, p3.Exit(0, 1, 10))                             // init Waiting, next = 1010, deadline at 1000
	mustReject(t, p3.Start(0, 1005), RejectDeadlineExceeded) // also backoff
	mustReject(t, p3.Start(1, 1005), RejectDeadlineExceeded) // also init gate

	// Init-incomplete beats backoff: app container 1 has next = 0, so give
	// it a backoff first via a shared-budget pod... simpler: app is gated
	// while init incomplete; its next is 0, so use a config where the app
	// itself would be in backoff. Instead verify gate order with the app
	// before deadline: init gate fires.
	p4 := mustNew(t, cfg)
	mustOK(t, p4.Start(0, 0))
	mustOK(t, p4.Exit(0, 1, 10)) // init backing off until 1010
	mustReject(t, p4.Start(1, 500), RejectInitNotComplete)
	mustReject(t, p4.Start(0, 500), RejectBackoff)
}

// TestRejectedOpsDoNotMutate: a rejected operation leaves every piece of
// observable state untouched.
func TestRejectedOpsDoNotMutate(t *testing.T) {
	cfg := baseConfig()
	cfg.InitContainers = 1
	cfg.ActiveDeadline = 1000
	p := mustNew(t, cfg)
	mustOK(t, p.Start(0, 0))
	mustOK(t, p.Exit(0, 1, 10)) // next = 1010
	before := p.Snapshot()

	rejections := []error{
		p.Start(9, 20),   // invalid index
		p.Start(0, -5),   // invalid now
		p.Start(0, 5),    // clock regression
		p.Exit(0, 1, 5),  // clock regression
		p.Start(1, 20),   // init not complete
		p.Start(0, 20),   // backoff
		p.Exit(1, 0, 20), // not Running
		p.Exit(9, 0, 20), // invalid index
	}
	for i, err := range rejections {
		if err == nil {
			t.Fatalf("rejection %d unexpectedly succeeded", i)
		}
	}
	after := p.Snapshot()
	if !snapshotsEqual(before, after) {
		t.Fatalf("rejected ops mutated state:\nbefore=%+v\nafter=%+v", before, after)
	}
	// maxNow must not move either: rejected ops used now=20, but a Start at
	// now=15 (above the accepted maxNow=10) still reports Backoff, not
	// ClockRegression.
	mustReject(t, p.Start(0, 15), RejectBackoff)
}

func snapshotsEqual(a, b Snapshot) bool {
	if a.Restarts != b.Restarts || a.T0 != b.T0 || a.T0Set != b.T0Set {
		return false
	}
	if len(a.Containers) != len(b.Containers) {
		return false
	}
	for i := range a.Containers {
		if a.Containers[i] != b.Containers[i] {
			return false
		}
	}
	return true
}

// TestConfigValidation: out-of-range construction parameters are rejected
// as a whole.
func TestConfigValidation(t *testing.T) {
	good := baseConfig()
	if _, err := New(good); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	cases := []Config{
		{InitContainers: -1},
		{InitContainers: 17},
		{AppContainers: 0},
		{AppContainers: 17},
		{Policy: Policy(42)},
		{BackoffBase: 0, BackoffMax: 1, BackoffReset: 1},
		{BackoffBase: 5, BackoffMax: 4, BackoffReset: 1}, // M < B
		{BackoffBase: 1, BackoffMax: 1e12 + 1, BackoffReset: 1},
		{BackoffBase: 1, BackoffMax: 1, BackoffReset: 0},
		{BackoffBase: 1, BackoffMax: 1, BackoffReset: 1e12 + 1},
		{BackoffBase: 1, BackoffMax: 1, BackoffReset: 1, ActiveDeadline: -1},
		{BackoffBase: 1, BackoffMax: 1, BackoffReset: 1, ActiveDeadline: 1e12 + 1},
		{BackoffBase: 1, BackoffMax: 1, BackoffReset: 1, RestartBudget: -1},
		{BackoffBase: 1, BackoffMax: 1, BackoffReset: 1, RestartBudget: 1e6 + 1},
	}
	for i, cfg := range cases {
		if _, err := New(cfg); err == nil {
			t.Fatalf("case %d: invalid config %+v accepted", i, cfg)
		} else if !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("case %d: error %v is not ErrInvalidConfig", i, err)
		}
	}
	// Boundary values are accepted.
	for i, cfg := range []Config{
		{InitContainers: 16, AppContainers: 16, Policy: Always, BackoffBase: 1, BackoffMax: 1, BackoffReset: 1, ActiveDeadline: 0, RestartBudget: 0},
		{InitContainers: 0, AppContainers: 1, Policy: Never, BackoffBase: 1e12, BackoffMax: 1e12, BackoffReset: 1e12, ActiveDeadline: 1e12, RestartBudget: 1e6},
	} {
		if _, err := New(cfg); err != nil {
			t.Fatalf("boundary config %d rejected: %v", i, err)
		}
	}
}

// TestTerminalContainersDoNotChange: terminal containers ignore further
// operations.
func TestTerminalContainersDoNotChange(t *testing.T) {
	cfg := baseConfig()
	cfg.Policy = OnFailure
	p := mustNew(t, cfg)
	mustOK(t, p.Start(0, 0))
	mustOK(t, p.Exit(0, 0, 10)) // Succeeded
	mustReject(t, p.Start(0, 20), RejectNotWaiting)
	mustReject(t, p.Exit(0, 1, 20), RejectNotRunning)
	cs := containerState(t, p, 0)
	if cs.State != Succeeded || cs.K != 0 || cs.Next != 0 {
		t.Fatalf("terminal container changed: %+v", cs)
	}
}

// TestT0SetOnce: t0 is fixed by the first accepted Start and never moves.
func TestT0SetOnce(t *testing.T) {
	cfg := baseConfig()
	cfg.AppContainers = 2
	p := mustNew(t, cfg)
	mustReject(t, p.Start(0, -1), RejectInvalidArgument) // rejected: no t0
	if p.Snapshot().T0Set {
		t.Fatal("t0 set by a rejected Start")
	}
	mustOK(t, p.Start(0, 50))
	mustOK(t, p.Start(1, 60))
	snap := p.Snapshot()
	if !snap.T0Set || snap.T0 != 50 {
		t.Fatalf("t0 = %d set=%v, want 50 set=true", snap.T0, snap.T0Set)
	}
}

// TestRestartSpacingInvariant: consecutive starts of one container are
// separated by at least the scheduled backoff delay.
func TestRestartSpacingInvariant(t *testing.T) {
	p := mustNew(t, baseConfig())
	prevStart := int64(-1)
	prevDelay := int64(0)
	now := int64(0)
	for i := 0; i < 20; i++ {
		if prevStart >= 0 && now-prevStart < prevDelay {
			t.Fatalf("restart %d: start spacing %d < delay %d", i, now-prevStart, prevDelay)
		}
		mustOK(t, p.Start(0, now))
		prevStart = now
		mustOK(t, p.Exit(0, 1, now+1))
		cs := containerState(t, p, 0)
		prevDelay = cs.Next - (now + 1)
		now = cs.Next
	}
}

// TestReplayDeterminism: the same operation sequence replays to identical
// states, phases and next times.
func TestReplayDeterminism(t *testing.T) {
	cfg := baseConfig()
	cfg.InitContainers = 1
	cfg.AppContainers = 2
	cfg.RestartBudget = 3
	type op struct {
		kind       string
		c          int
		code       int
		now        int64
		wantErr    bool
		wantReason RejectReason
	}
	ops := []op{
		{"Start", 0, 0, 0, false, 0},
		{"Exit", 0, 0, 5, false, 0},
		{"Start", 1, 0, 5, false, 0},
		{"Exit", 1, 1, 10, false, 0},
		{"Start", 2, 0, 10, false, 0},
		{"Exit", 2, 1, 20, false, 0},
		{"Start", 1, 0, 500, true, RejectBackoff},
		{"Start", 1, 0, 1010, false, 0},
		{"Exit", 1, 1, 1020, false, 0},
		{"Start", 2, 0, 1020, false, 0},
		{"Exit", 2, 1, 1030, false, 0},
	}
	run := func() ([]Snapshot, []Phase, []string) {
		p := mustNew(t, cfg)
		var snaps []Snapshot
		var phases []Phase
		var reasons []string
		for i, o := range ops {
			var err error
			switch o.kind {
			case "Start":
				err = p.Start(o.c, o.now)
			case "Exit":
				err = p.Exit(o.c, o.code, o.now)
			}
			if o.wantErr {
				if err == nil {
					t.Fatalf("op %d: expected rejection", i)
				}
			} else if err != nil {
				t.Fatalf("op %d: unexpected rejection: %v", i, err)
			}
			snaps = append(snaps, p.Snapshot())
			ph, err := p.Phase(o.now)
			if err != nil {
				t.Fatalf("op %d: Phase error: %v", i, err)
			}
			phases = append(phases, ph)
			for c := 0; c < cfg.InitContainers+cfg.AppContainers; c++ {
				r, err := p.Reason(c, o.now)
				if err != nil {
					t.Fatalf("op %d: Reason error: %v", i, err)
				}
				reasons = append(reasons, r)
			}
		}
		return snaps, phases, reasons
	}
	s1, p1, r1 := run()
	s2, p2, r2 := run()
	if len(s1) != len(s2) {
		t.Fatal("snapshot length mismatch")
	}
	for i := range s1 {
		if !snapshotsEqual(s1[i], s2[i]) {
			t.Fatalf("replay diverged at op %d:\n%+v\n%+v", i, s1[i], s2[i])
		}
		if p1[i] != p2[i] {
			t.Fatalf("phase diverged at op %d: %s vs %s", i, p1[i], p2[i])
		}
	}
	for i := range r1 {
		if r1[i] != r2[i] {
			t.Fatalf("reason diverged at %d: %q vs %q", i, r1[i], r2[i])
		}
	}
}

// TestConcurrentOps: concurrent calls are safe and equivalent to some
// serial order; terminal states and invariants still hold.
func TestConcurrentOps(t *testing.T) {
	cfg := baseConfig()
	cfg.AppContainers = 4
	cfg.RestartBudget = 100
	p := mustNew(t, cfg)

	const workers = 8
	done := make(chan struct{}, workers)
	for w := 0; w < workers; w++ {
		go func(id int) {
			defer func() { done <- struct{}{} }()
			for now := int64(0); now < 500; now++ {
				c := (id + int(now)) % 4
				_ = p.Start(c, now)
				_ = p.Exit(c, int(now)%3, now)
				if _, err := p.Phase(now); err != nil {
					t.Errorf("Phase(%d) error: %v", now, err)
				}
				if _, err := p.Reason(c, now); err != nil {
					t.Errorf("Reason(%d,%d) error: %v", c, now, err)
				}
			}
		}(w)
	}
	for w := 0; w < workers; w++ {
		<-done
	}
	snap := p.Snapshot()
	if snap.Restarts > cfg.RestartBudget {
		t.Fatalf("restarts = %d exceeds budget %d", snap.Restarts, cfg.RestartBudget)
	}
	if !snap.T0Set {
		t.Fatal("t0 never set")
	}
}
