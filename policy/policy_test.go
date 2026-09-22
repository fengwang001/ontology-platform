package policy

import (
	"errors"
	"testing"
	"time"

	"ontology/journal"
	"ontology/step"
)

func machineWithExec(n int) *step.Machine {
	m := step.NewMachine()
	m.Apply(journal.Record{Seq: 1, Phase: journal.PhaseStart, StepID: "s"})
	for i := 2; i <= n; i++ {
		m.Apply(journal.Record{Seq: uint64(i), Phase: journal.PhaseRetry, StepID: "s"})
	}
	return m
}

// N retries means N+1 total executions: retries are allowed while the
// failed-attempt count is in [1, N], and attempt N+1 is terminal.
func TestRetryBoundaryLeftClosedRightOpen(t *testing.T) {
	const n = 3
	p := Policy{MaxRetries: n}
	err := errors.New("boom")
	for exec := 1; exec <= n; exec++ {
		if !p.AllowRetry(machineWithExec(exec), err) {
			t.Fatalf("failure %d should be retryable", exec)
		}
	}
	if p.AllowRetry(machineWithExec(n+1), err) {
		t.Fatalf("failure %d must be terminal", n+1)
	}
}

func TestNonRetryableErrorFailsImmediately(t *testing.T) {
	fatal := errors.New("fatal")
	p := Policy{
		MaxRetries: 5,
		Retryable:  func(err error) bool { return !errors.Is(err, fatal) },
	}
	if p.AllowRetry(machineWithExec(1), fatal) {
		t.Fatal("non-retryable error must not be retried")
	}
	if !p.AllowRetry(machineWithExec(1), errors.New("transient")) {
		t.Fatal("transient error should be retried")
	}
}

func TestNilRetryableMeansEverythingRetries(t *testing.T) {
	p := Policy{MaxRetries: 1}
	if !p.AllowRetry(machineWithExec(1), errors.New("x")) {
		t.Fatal("nil Retryable should retry any error")
	}
}

func TestCompensateRetryBoundary(t *testing.T) {
	p := Policy{CompensateRetries: 2}
	if !p.AllowCompensateRetry(1) || !p.AllowCompensateRetry(2) {
		t.Fatal("compensation attempts 1 and 2 should be retryable")
	}
	if p.AllowCompensateRetry(3) {
		t.Fatal("compensation attempt 3 must be terminal")
	}
}

func TestFakeClockAccumulatesBackoff(t *testing.T) {
	c := NewFakeClock(time.Unix(0, 0))
	p := Policy{Backoff: func(attempt int) time.Duration {
		return time.Duration(attempt) * time.Second
	}}
	for attempt := 1; attempt <= 3; attempt++ {
		c.Sleep(p.Wait(attempt))
	}
	if c.Slept() != 6*time.Second {
		t.Fatalf("slept = %v", c.Slept())
	}
	if c.Now() != time.Unix(6, 0) {
		t.Fatalf("now = %v", c.Now())
	}
	if p.Wait(1) != time.Second {
		t.Fatal("backoff mismatch")
	}
	var zero Policy
	if zero.Wait(5) != 0 {
		t.Fatal("nil Backoff must mean no wait")
	}
}
