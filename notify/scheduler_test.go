package notify

import (
	"errors"
	"reflect"
	"testing"
)

func constJitter(v int64) Jitter {
	return func(string, int64, int64) int64 { return v }
}

func mustNew(t *testing.T, base, capVal, m, a, raCap, k, cool int64, j Jitter) *Scheduler {
	t.Helper()
	s, err := NewScheduler(base, capVal, m, a, raCap, k, cool, j)
	if err != nil {
		t.Fatalf("NewScheduler(%d,%d,%d,%d,%d,%d,%d): %v", base, capVal, m, a, raCap, k, cool, err)
	}
	return s
}

func mustSubmit(t *testing.T, s *Scheduler, id string, chain []string, created, ttl int64) {
	t.Helper()
	if err := s.Submit(id, chain, created, ttl); err != nil {
		t.Fatalf("Submit(%s): %v", id, err)
	}
}

func mustFail(t *testing.T, s *Scheduler, id string, now int64, class Class, ra int64) Outcome {
	t.Helper()
	o, err := s.Fail(id, now, class, ra)
	if err != nil {
		t.Fatalf("Fail(%s,%d,%s,%d): %v", id, now, class, ra, err)
	}
	return o
}

func wantOutcome(t *testing.T, got Outcome, ch string, nextAt int64) {
	t.Helper()
	if got.Dead || got.Channel != ch || got.NextAt != nextAt {
		t.Fatalf("got %+v, want channel=%q nextAt=%d", got, ch, nextAt)
	}
}

func wantDead(t *testing.T, got Outcome, reason Reason) {
	t.Helper()
	if !got.Dead || got.Reason != reason {
		t.Fatalf("got %+v, want dead reason=%q", got, reason)
	}
}

func wantErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("got err %v, want %v", err, want)
	}
}

// The worked example from the specification.
func TestSpecExample(t *testing.T) {
	s := mustNew(t, 10, 40, 3, 5, 60, 1000, 10, constJitter(3))
	mustSubmit(t, s, "t1", []string{"push", "sms"}, 0, 1000)

	// transient@0: n=1, d=10, jitter clamped to 2, w=8.
	wantOutcome(t, mustFail(t, s, "t1", 0, ClassTransient, 0), "push", 8)
	// transient@8: n=2, d=20, jitter 3, w=17.
	wantOutcome(t, mustFail(t, s, "t1", 8, ClassTransient, 0), "push", 25)
	// throttled(ra=30)@25: att=3, n=3>=M, switch to sms, w=0.
	wantOutcome(t, mustFail(t, s, "t1", 25, ClassThrottled, 30), "sms", 25)
	// throttled(ra=50)@25: att=4, n=1, d'=8, w=max(8,50)=50.
	wantOutcome(t, mustFail(t, s, "t1", 25, ClassThrottled, 50), "sms", 75)
	// transient@75: att=5>=A, dead budget.
	wantDead(t, mustFail(t, s, "t1", 75, ClassTransient, 0), ReasonBudget)
}

// Same example, but the sms failure at 25 has ra=100 > RAcap: switch branch
// finds no later channel and the task dies as throttled.
func TestSpecExampleThrottledDead(t *testing.T) {
	s := mustNew(t, 10, 40, 3, 5, 60, 1000, 10, constJitter(3))
	mustSubmit(t, s, "t1", []string{"push", "sms"}, 0, 1000)
	wantOutcome(t, mustFail(t, s, "t1", 0, ClassTransient, 0), "push", 8)
	wantOutcome(t, mustFail(t, s, "t1", 8, ClassTransient, 0), "push", 25)
	wantOutcome(t, mustFail(t, s, "t1", 25, ClassThrottled, 30), "sms", 25)
	wantDead(t, mustFail(t, s, "t1", 25, ClassThrottled, 100), ReasonThrottled)
}

// The circuit-breaking example from the specification.
func TestSpecCircuitExample(t *testing.T) {
	s := mustNew(t, 1, 4, 3, 10, 0, 2, 100, constJitter(0))
	mustSubmit(t, s, "a", []string{"push", "sms"}, 0, 100000)
	// Move task a to sms without touching gfail (permanent does not count).
	wantOutcome(t, mustFail(t, s, "a", 0, ClassPermanent, 0), "sms", 0)
	// Two transient failures on sms at 10 and 20 trip the breaker.
	wantOutcome(t, mustFail(t, s, "a", 10, ClassTransient, 0), "sms", 11)
	mustFail(t, s, "a", 20, ClassTransient, 0)
	if got := s.openUntil["sms"]; got != 120 {
		t.Fatalf("openUntil[sms]=%d, want 120", got)
	}

	// Another task on push gets permanent at 30: sms is open, skip to email.
	mustSubmit(t, s, "b", []string{"push", "sms", "email"}, 0, 100000)
	wantOutcome(t, mustFail(t, s, "b", 30, ClassPermanent, 0), "email", 30)

	// At 120 the breaker is no longer open (openUntil == now is not open).
	mustSubmit(t, s, "c", []string{"push", "sms", "email"}, 0, 100000)
	wantOutcome(t, mustFail(t, s, "c", 120, ClassPermanent, 0), "sms", 120)
}

// d is capped exactly at Cap.
func TestBackoffCappedAtCap(t *testing.T) {
	s := mustNew(t, 10, 40, 16, 100, 0, 1000, 10, constJitter(0))
	mustSubmit(t, s, "t1", []string{"push"}, 0, 100000)
	// n=1: d=10; n=2: d=20; n=3: d=40 == Cap exactly.
	wantOutcome(t, mustFail(t, s, "t1", 0, ClassTransient, 0), "push", 10)
	wantOutcome(t, mustFail(t, s, "t1", 10, ClassTransient, 0), "push", 30)
	wantOutcome(t, mustFail(t, s, "t1", 30, ClassTransient, 0), "push", 70)
	// n=4: d would be 80, capped to 40.
	wantOutcome(t, mustFail(t, s, "t1", 70, ClassTransient, 0), "push", 110)
}

// Jitter is clamped to [0, floor(d/4)] on both ends.
func TestJitterClamping(t *testing.T) {
	// d = Base = 40 at n=1, floor(d/4) = 10.
	// Negative jitter clamps to 0.
	s := mustNew(t, 40, 1000, 16, 100, 0, 1000, 10, constJitter(-5))
	mustSubmit(t, s, "neg", []string{"push"}, 0, 100000)
	wantOutcome(t, mustFail(t, s, "neg", 0, ClassTransient, 0), "push", 40)

	// Jitter above d/4 clamps to floor(d/4) = 10.
	s = mustNew(t, 40, 1000, 16, 100, 0, 1000, 10, constJitter(999))
	mustSubmit(t, s, "over", []string{"push"}, 0, 100000)
	wantOutcome(t, mustFail(t, s, "over", 0, ClassTransient, 0), "push", 30)

	// Jitter exactly floor(d/4) is used as-is.
	s = mustNew(t, 40, 1000, 16, 100, 0, 1000, 10, constJitter(10))
	mustSubmit(t, s, "exact", []string{"push"}, 0, 100000)
	wantOutcome(t, mustFail(t, s, "exact", 0, ClassTransient, 0), "push", 30)
}

// w = max(d', ra): the larger of the backoff wait and Retry-After wins.
func TestWaitTakesMaxOfBackoffAndRA(t *testing.T) {
	// ra smaller than d'=8: wait d'.
	s := mustNew(t, 10, 40, 16, 100, 1000, 1000, 10, constJitter(2))
	mustSubmit(t, s, "small", []string{"push"}, 0, 100000)
	wantOutcome(t, mustFail(t, s, "small", 0, ClassThrottled, 3), "push", 8)

	// ra larger than d'=8: wait ra.
	s = mustNew(t, 10, 40, 16, 100, 1000, 1000, 10, constJitter(2))
	mustSubmit(t, s, "large", []string{"push"}, 0, 100000)
	wantOutcome(t, mustFail(t, s, "large", 0, ClassThrottled, 50), "push", 50)
}

// ra == RAcap waits; ra == RAcap+1 switches.
func TestRACapBoundary(t *testing.T) {
	s := mustNew(t, 10, 40, 16, 100, 60, 1000, 10, constJitter(0))
	mustSubmit(t, s, "eq", []string{"push", "sms"}, 0, 100000)
	wantOutcome(t, mustFail(t, s, "eq", 0, ClassThrottled, 60), "push", 60)

	s = mustNew(t, 10, 40, 16, 100, 60, 1000, 10, constJitter(0))
	mustSubmit(t, s, "over", []string{"push", "sms"}, 0, 100000)
	wantOutcome(t, mustFail(t, s, "over", 0, ClassThrottled, 61), "sms", 0)
}

// n == M switches immediately without waiting, n resets to 0, and att is
// kept across the switch (budget counts failures on all channels).
func TestExhaustedSwitchResetsNKeepsAtt(t *testing.T) {
	s := mustNew(t, 10, 40, 3, 5, 0, 1000, 10, constJitter(0))
	mustSubmit(t, s, "t1", []string{"push", "sms"}, 0, 100000)
	wantOutcome(t, mustFail(t, s, "t1", 0, ClassTransient, 0), "push", 10)
	wantOutcome(t, mustFail(t, s, "t1", 10, ClassTransient, 0), "push", 30)
	// n reaches M=3: switch immediately, nextAt == now.
	wantOutcome(t, mustFail(t, s, "t1", 30, ClassTransient, 0), "sms", 30)
	// n was reset: d = Base again, so nextAt = 10 + 10.
	wantOutcome(t, mustFail(t, s, "t1", 30, ClassTransient, 0), "sms", 40)
	// att kept across the switch: this 5th failure hits A=5.
	wantDead(t, mustFail(t, s, "t1", 40, ClassTransient, 0), ReasonBudget)
}

// permanent switches on a middle channel and dies permanent on the last one.
func TestPermanentSwitchVsDead(t *testing.T) {
	s := mustNew(t, 10, 40, 2, 100, 0, 1000, 10, constJitter(0))
	mustSubmit(t, s, "mid", []string{"push", "sms"}, 0, 100000)
	wantOutcome(t, mustFail(t, s, "mid", 0, ClassPermanent, 0), "sms", 0)
	wantDead(t, mustFail(t, s, "mid", 0, ClassPermanent, 0), ReasonPermanent)
}

// Reason priority: a failing switch reports its own reason even when the
// budget is also exhausted; budget wins over expired.
func TestReasonPriority(t *testing.T) {
	// Switch reason before budget: A=1, permanent on the only channel.
	s := mustNew(t, 10, 40, 2, 1, 0, 1000, 10, constJitter(0))
	mustSubmit(t, s, "sw", []string{"push"}, 0, 100000)
	wantDead(t, mustFail(t, s, "sw", 0, ClassPermanent, 0), ReasonPermanent)

	// Budget before expired: A=1, backoff would pass the deadline, but the
	// budget check fires first.
	s = mustNew(t, 10, 40, 2, 1, 0, 1000, 10, constJitter(0))
	mustSubmit(t, s, "bud", []string{"push"}, 0, 1)
	wantDead(t, mustFail(t, s, "bud", 0, ClassTransient, 0), ReasonBudget)

	// A successful switch still dies of budget when att >= A afterwards.
	s = mustNew(t, 10, 40, 2, 1, 0, 1000, 10, constJitter(0))
	mustSubmit(t, s, "swb", []string{"push", "sms"}, 0, 100000)
	wantDead(t, mustFail(t, s, "swb", 0, ClassPermanent, 0), ReasonBudget)
}

// nextAt == deadline is allowed; deadline+1 dies expired.
func TestDeadlineBoundary(t *testing.T) {
	s := mustNew(t, 10, 40, 16, 100, 0, 1000, 10, constJitter(0))
	mustSubmit(t, s, "eq", []string{"push"}, 0, 10)
	wantOutcome(t, mustFail(t, s, "eq", 0, ClassTransient, 0), "push", 10)

	s = mustNew(t, 10, 40, 16, 100, 0, 1000, 10, constJitter(0))
	mustSubmit(t, s, "over", []string{"push"}, 0, 9)
	wantDead(t, mustFail(t, s, "over", 0, ClassTransient, 0), ReasonExpired)
}

// gfail reaching exactly K trips the breaker; Success clears gfail but does
// not lift an existing breaker.
func TestCircuitTripAndSuccessReset(t *testing.T) {
	s := mustNew(t, 1, 4, 16, 100, 0, 2, 100, constJitter(0))
	mustSubmit(t, s, "a", []string{"push"}, 0, 100000)
	mustFail(t, s, "a", 0, ClassTransient, 0) // gfail=1, no trip
	if s.openUntil["push"] != 0 {
		t.Fatalf("breaker tripped below K")
	}
	mustFail(t, s, "a", 1, ClassTransient, 0) // gfail=2 == K, trips
	if got := s.openUntil["push"]; got != 101 {
		t.Fatalf("openUntil=%d, want 101", got)
	}

	// Success clears gfail but keeps the breaker: a switching task still
	// skips push while now < 101, and gfail starts counting from 0 again.
	if err := s.Success("a", 3); err != nil {
		t.Fatalf("Success: %v", err)
	}
	if s.gfail["push"] != 0 {
		t.Fatalf("gfail not cleared by Success")
	}
	if s.openUntil["push"] != 101 {
		t.Fatalf("breaker lifted by Success")
	}
	mustSubmit(t, s, "b", []string{"email", "push", "sms"}, 0, 100000)
	// permanent on email at 50: push is still open until 101, skip to sms.
	wantOutcome(t, mustFail(t, s, "b", 50, ClassPermanent, 0), "sms", 50)
	// permanent on the last channel: no later channel, dead permanent.
	wantDead(t, mustFail(t, s, "b", 50, ClassPermanent, 0), ReasonPermanent)

	// After the breaker expires, one transient failure (gfail=1 < K) does
	// not re-trip; the second does.
	mustSubmit(t, s, "c", []string{"push"}, 0, 100000)
	mustFail(t, s, "c", 101, ClassTransient, 0)
	if s.openUntil["push"] != 101 {
		t.Fatalf("breaker re-tripped below K")
	}
	mustFail(t, s, "c", 102, ClassTransient, 0)
	if got := s.openUntil["push"]; got != 202 {
		t.Fatalf("openUntil=%d, want 202", got)
	}
}

// permanent failures never increment gfail.
func TestPermanentDoesNotCountGfail(t *testing.T) {
	s := mustNew(t, 1, 4, 16, 100, 0, 1, 100, constJitter(0))
	mustSubmit(t, s, "a", []string{"push", "sms"}, 0, 100000)
	// K=1: any counted failure would trip the breaker immediately.
	wantOutcome(t, mustFail(t, s, "a", 0, ClassPermanent, 0), "sms", 0)
	if s.gfail["push"] != 0 || s.openUntil["push"] != 0 {
		t.Fatalf("permanent counted towards gfail")
	}
}

// Rejected operations must not change any state.
func TestRejectionsDoNotChangeState(t *testing.T) {
	s := mustNew(t, 10, 40, 3, 5, 60, 1000, 10, constJitter(3))
	mustSubmit(t, s, "t1", []string{"push", "sms"}, 0, 1000)
	wantOutcome(t, mustFail(t, s, "t1", 5, ClassTransient, 0), "push", 13)

	snap := *s.tasks["t1"]
	gfail := s.gfail["push"]
	maxNow := s.maxNow

	rejections := []error{
		func() error { _, e := s.Fail("t1", 6, "bogus", 0); return e }(),
		func() error { _, e := s.Fail("t1", 6, ClassTransient, 1); return e }(),
		func() error { _, e := s.Fail("t1", 6, ClassThrottled, 1_000_000_001); return e }(),
		func() error { _, e := s.Fail("t1", -1, ClassTransient, 0); return e }(),
		func() error { _, e := s.Fail("ghost", 6, ClassTransient, 0); return e }(),
		func() error { _, e := s.Fail("t1", 4, ClassTransient, 0); return e }(),
		func() error { _, e := s.Fail("t1", 12, ClassTransient, 0); return e }(),
		func() error { return s.Success("ghost", 6) }(),
		func() error { return s.Success("t1", 4) }(),
		func() error { return s.Success("t1", 12) }(),
		func() error { return s.Submit("t1", []string{"push"}, 0, 10) }(),
		func() error { return s.Submit("bad", []string{"push", "push"}, 0, 10) }(),
		func() error { return s.Submit("", []string{"push"}, 0, 10) }(),
	}
	want := []error{
		ErrInvalidArgument, ErrInvalidArgument, ErrInvalidArgument, ErrInvalidArgument,
		ErrTaskNotFound,
		ErrClockRegression,
		ErrTooEarly,
		ErrTaskNotFound,
		ErrClockRegression,
		ErrTooEarly,
		ErrDuplicateTask,
		ErrInvalidArgument,
		ErrInvalidArgument,
	}
	for i := range rejections {
		if !errors.Is(rejections[i], want[i]) {
			t.Fatalf("rejection %d: got %v, want %v", i, rejections[i], want[i])
		}
	}
	if got := *s.tasks["t1"]; !reflect.DeepEqual(got, snap) {
		t.Fatalf("task mutated by rejections: %+v -> %+v", snap, got)
	}
	if s.gfail["push"] != gfail || s.maxNow != maxNow {
		t.Fatalf("global state mutated by rejections")
	}

	// The task still proceeds exactly as if the rejections never happened.
	wantOutcome(t, mustFail(t, s, "t1", 13, ClassTransient, 0), "push", 30)
}

// Rejection reasons are reported in a fixed priority order.
func TestRejectionPriority(t *testing.T) {
	s := mustNew(t, 10, 40, 3, 5, 60, 1000, 10, constJitter(0))
	mustSubmit(t, s, "t1", []string{"push"}, 0, 1000)
	mustFail(t, s, "t1", 5, ClassTransient, 0) // nextAt=15, maxNow=5

	// invalid argument beats not-found.
	_, err := s.Fail("ghost", 6, "bogus", 0)
	wantErr(t, err, ErrInvalidArgument)
	// not-found beats finished/clock/early.
	_, err = s.Fail("ghost", 1, ClassTransient, 0)
	wantErr(t, err, ErrTaskNotFound)
	// clock regression beats too-early.
	_, err = s.Fail("t1", 4, ClassTransient, 0)
	wantErr(t, err, ErrClockRegression)
	// finished beats clock regression and too-early.
	if err := s.Success("t1", 15); err != nil {
		t.Fatalf("Success: %v", err)
	}
	_, err = s.Fail("t1", 0, ClassTransient, 0)
	wantErr(t, err, ErrTaskFinished)
	wantErr(t, s.Success("t1", 20), ErrTaskFinished)
}

// Constructor and Submit parameter validation.
func TestValidation(t *testing.T) {
	j := constJitter(0)
	bad := [][7]int64{
		{0, 10, 1, 1, 0, 1, 1},                // base < 1
		{1_000_001, 2_000_000, 1, 1, 0, 1, 1}, // base > 1e6
		{10, 9, 1, 1, 0, 1, 1},                // cap < base
		{1, 1_000_000_001, 1, 1, 0, 1, 1},     // cap > 1e9
		{1, 10, 0, 1, 0, 1, 1},                // m < 1
		{1, 10, 17, 1, 0, 1, 1},               // m > 16
		{1, 10, 1, 0, 0, 1, 1},                // a < 1
		{1, 10, 1, 101, 0, 1, 1},              // a > 100
		{1, 10, 1, 1, -1, 1, 1},               // raCap < 0
		{1, 10, 1, 1, 1_000_000_001, 1, 1},    // raCap > 1e9
		{1, 10, 1, 1, 0, 0, 1},                // k < 1
		{1, 10, 1, 1, 0, 1001, 1},             // k > 1000
		{1, 10, 1, 1, 0, 1, 0},                // cool < 1
		{1, 10, 1, 1, 0, 1, 1_000_000_001},    // cool > 1e9
	}
	for i, p := range bad {
		if _, err := NewScheduler(p[0], p[1], p[2], p[3], p[4], p[5], p[6], j); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("case %d: want ErrInvalidArgument", i)
		}
	}
	if _, err := NewScheduler(1, 10, 1, 1, 0, 1, 1, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("nil jitter: want ErrInvalidArgument")
	}

	s := mustNew(t, 1, 10, 1, 1, 0, 1, 1, j)
	wantErr(t, s.Submit("x", nil, 0, 1), ErrInvalidArgument)
	wantErr(t, s.Submit("x", []string{"a", "b", "c", "d", "e"}, 0, 1), ErrInvalidArgument)
	wantErr(t, s.Submit("x", []string{""}, 0, 1), ErrInvalidArgument)
	wantErr(t, s.Submit("x", []string{"a"}, -1, 1), ErrInvalidArgument)
	wantErr(t, s.Submit("x", []string{"a"}, 1_000_000_000_001, 1), ErrInvalidArgument)
	wantErr(t, s.Submit("x", []string{"a"}, 0, 0), ErrInvalidArgument)
	wantErr(t, s.Submit("x", []string{"a"}, 0, 1_000_000_001), ErrInvalidArgument)
}
