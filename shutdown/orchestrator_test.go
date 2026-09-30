package shutdown

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

type threadSafeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *threadSafeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *threadSafeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func testOrchestrator(t *testing.T) (*Orchestrator, *threadSafeBuffer) {
	t.Helper()
	logs := &threadSafeBuffer{}
	logger := slog.New(slog.NewTextHandler(io.MultiWriter(logs), &slog.HandlerOptions{Level: slog.LevelInfo}))
	return New(logger), logs
}

func mustRegister(t *testing.T, o *Orchestrator, at int64, id string, grace int64, dependencies ...string) {
	t.Helper()
	if err := o.Register(at, id, grace, dependencies...); err != nil {
		t.Fatalf("Register(%q) = %v", id, err)
	}
}

func requireErrorIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want %v", err, target)
	}
}

func requireState(t *testing.T, o *Orchestrator, at int64, id string, terminationAt int64, stopAt int64, method StopMethod) {
	t.Helper()
	state, err := o.Status(id, at)
	if err != nil {
		t.Fatalf("Status(%q) at %d = %v", id, at, err)
	}
	if state.TerminationTime != terminationAt || state.StopTime != stopAt || state.StopMethod != method {
		t.Fatalf("Status(%q) = %+v, want termination=%d stop=%d method=%v", id, state, terminationAt, stopAt, method)
	}
}

func requireLogContains(t *testing.T, logs *threadSafeBuffer, want ...string) {
	t.Helper()
	got := logs.String()
	for _, fragment := range want {
		if !strings.Contains(got, fragment) {
			t.Fatalf("logs do not contain %q\nlogs:\n%s", fragment, got)
		}
	}
}

func requireLogOrder(t *testing.T, logs *threadSafeBuffer, fragments ...string) {
	t.Helper()
	got := logs.String()
	position := 0
	for _, fragment := range fragments {
		next := strings.Index(got[position:], fragment)
		if next < 0 {
			t.Fatalf("logs do not contain %q after position %d\nlogs:\n%s", fragment, position, got)
		}
		position += next + len(fragment)
	}
}

func TestDiamondUsesLatestDependentStopTime(t *testing.T) {
	o, logs := testOrchestrator(t)

	mustRegister(t, o, 1, "base", 100)
	mustRegister(t, o, 2, "left", 100, "base")
	mustRegister(t, o, 3, "right", 100, "base")
	mustRegister(t, o, 4, "top", 100, "left", "right")

	if err := o.BeginShutdown(10); err != nil {
		t.Fatalf("BeginShutdown() = %v", err)
	}
	if err := o.ReportExit("top", 12); err != nil {
		t.Fatalf("ReportExit(top) = %v", err)
	}
	if err := o.ReportExit("left", 15); err != nil {
		t.Fatalf("ReportExit(left) = %v", err)
	}
	if err := o.ReportExit("right", 20); err != nil {
		t.Fatalf("ReportExit(right) = %v", err)
	}

	requireState(t, o, 25, "top", 10, 12, Graceful)
	requireState(t, o, 25, "left", 12, 15, Graceful)
	requireState(t, o, 25, "right", 12, 20, Graceful)
	requireState(t, o, 25, "base", 20, 0, NotStopped)

	requireLogContains(t, logs,
		`msg="begin shutdown input" at=10`,
		`msg="termination signaled" id=top termination_at=10 kill_deadline_at=110`,
		`msg="termination signaled" id=left termination_at=12 kill_deadline_at=112`,
		`msg="termination signaled" id=right termination_at=12 kill_deadline_at=112`,
		`msg="termination signaled" id=base termination_at=20 kill_deadline_at=120`,
	)
}

func TestReportAtExactGraceDeadlineIsKilledAndRejected(t *testing.T) {
	o, logs := testOrchestrator(t)

	mustRegister(t, o, 1, "service", 10)
	if err := o.BeginShutdown(100); err != nil {
		t.Fatalf("BeginShutdown() = %v", err)
	}

	err := o.ReportExit("service", 110)
	requireErrorIs(t, err, ErrAlreadyStopped)
	requireState(t, o, 110, "service", 100, 110, Killed)

	requireLogContains(t, logs,
		`msg="kill deadline reached" id=service deadline_at=110`,
		`msg="service stopped" id=service stop_at=110 method=killed`,
		`msg="report exit rejected" id=service stop_at=110 reason="shutdown: service has already stopped"`,
	)
}

func TestTwoLevelKillCascade(t *testing.T) {
	o, logs := testOrchestrator(t)

	mustRegister(t, o, 1, "leaf", 5)
	mustRegister(t, o, 2, "middle", 7, "leaf")
	mustRegister(t, o, 3, "root", 10, "middle")
	if err := o.BeginShutdown(10); err != nil {
		t.Fatalf("BeginShutdown() = %v", err)
	}

	states, err := o.Snapshot(35)
	if err != nil {
		t.Fatalf("Snapshot() = %v", err)
	}
	if states["root"].TerminationTime != 10 || states["root"].StopTime != 20 || states["root"].StopMethod != Killed {
		t.Fatalf("root state = %+v, want signal 10 stop 20 killed", states["root"])
	}
	if states["middle"].TerminationTime != 20 || states["middle"].StopTime != 27 || states["middle"].StopMethod != Killed {
		t.Fatalf("middle state = %+v, want signal 20 stop 27 killed", states["middle"])
	}
	if states["leaf"].TerminationTime != 27 || states["leaf"].StopTime != 32 || states["leaf"].StopMethod != Killed {
		t.Fatalf("leaf state = %+v, want signal 27 stop 32 killed", states["leaf"])
	}

	requireLogContains(t, logs,
		`msg="kill deadline reached" id=root deadline_at=20`,
		`msg="termination signaled" id=middle termination_at=20 kill_deadline_at=27`,
		`msg="kill deadline reached" id=middle deadline_at=27`,
		`msg="termination signaled" id=leaf termination_at=27 kill_deadline_at=32`,
		`msg="kill deadline reached" id=leaf deadline_at=32`,
	)
}

func TestOneCallSettlesMultipleDueKillsInTimeOrder(t *testing.T) {
	o, logs := testOrchestrator(t)

	mustRegister(t, o, 1, "later", 10)
	mustRegister(t, o, 2, "earlier", 5)
	if err := o.BeginShutdown(10); err != nil {
		t.Fatalf("BeginShutdown() = %v", err)
	}

	states, err := o.Snapshot(20)
	if err != nil {
		t.Fatalf("Snapshot() = %v", err)
	}
	if states["earlier"].StopTime != 15 || states["earlier"].StopMethod != Killed {
		t.Fatalf("earlier = %+v, want stop 15 killed", states["earlier"])
	}
	if states["later"].StopTime != 20 || states["later"].StopMethod != Killed {
		t.Fatalf("later = %+v, want stop 20 killed", states["later"])
	}

	requireLogOrder(t, logs,
		`msg="kill deadline reached" id=earlier deadline_at=15`,
		`msg="service stopped" id=earlier stop_at=15 method=killed`,
		`msg="kill deadline reached" id=later deadline_at=20`,
		`msg="service stopped" id=later stop_at=20 method=killed`,
	)
}

func TestUpstreamGracefulThenDownstreamKilled(t *testing.T) {
	o, logs := testOrchestrator(t)

	mustRegister(t, o, 1, "downstream", 10)
	mustRegister(t, o, 2, "upstream", 20, "downstream")
	if err := o.BeginShutdown(10); err != nil {
		t.Fatalf("BeginShutdown() = %v", err)
	}
	if err := o.ReportExit("upstream", 15); err != nil {
		t.Fatalf("ReportExit(upstream) = %v", err)
	}

	states, err := o.Snapshot(30)
	if err != nil {
		t.Fatalf("Snapshot() = %v", err)
	}
	if states["upstream"].TerminationTime != 10 || states["upstream"].StopTime != 15 || states["upstream"].StopMethod != Graceful {
		t.Fatalf("upstream = %+v, want graceful at 15", states["upstream"])
	}
	if states["downstream"].TerminationTime != 15 || states["downstream"].StopTime != 25 || states["downstream"].StopMethod != Killed {
		t.Fatalf("downstream = %+v, want signal 15 and kill 25", states["downstream"])
	}

	requireLogContains(t, logs,
		`msg="report exit accepted as graceful" id=upstream at=15 deadline_at=30`,
		`msg="stale kill skipped" id=upstream deadline_at=30 reason="already stopped"`,
		`msg="termination signaled" id=downstream termination_at=15 kill_deadline_at=25`,
		`msg="kill deadline reached" id=downstream deadline_at=25`,
	)
}

func TestValidationErrorsAndRejectedRegistrationHasNoStateChange(t *testing.T) {
	o, _ := testOrchestrator(t)

	requireErrorIs(t, o.Register(1, "bad", 0), ErrInvalidGracePeriod)
	mustRegister(t, o, 1, "known", 10)
	mustRegister(t, o, 2, "waiting", 10, "known")
	mustRegister(t, o, 3, "blocked", 10, "waiting")
	requireErrorIs(t, o.Register(4, "known", 10), ErrDuplicateService)
	requireErrorIs(t, o.Register(5, "missing", 10, "unknown"), ErrUnknownDependency)
	requireErrorIs(t, o.Register(6, "self", 10, "self"), ErrDependencyCycle)

	if err := o.BeginShutdown(10); err != nil {
		t.Fatalf("BeginShutdown() = %v", err)
	}
	requireErrorIs(t, o.BeginShutdown(11), ErrShutdownAlreadyDone)
	requireErrorIs(t, o.Register(12, "late", 10), ErrShutdownStarted)
	requireErrorIs(t, o.Register(9, "back", 10), ErrTimeRewound)

	if _, err := o.Status("bad", 13); !errors.Is(err, ErrServiceNotFound) {
		t.Fatalf("Status(bad) error = %v, want ErrServiceNotFound", err)
	}
	if _, err := o.Status("late", 14); !errors.Is(err, ErrServiceNotFound) {
		t.Fatalf("Status(late) error = %v, want ErrServiceNotFound", err)
	}
	requireErrorIs(t, o.ReportExit("ghost", 15), ErrServiceNotFound)
	requireErrorIs(t, o.ReportExit("known", 16), ErrNoTerminationSignal)
	requireErrorIs(t, o.ReportExit("known", 15), ErrTimeRewound)
}

func TestConcurrentStatusCallsAreSerialized(t *testing.T) {
	o, _ := testOrchestrator(t)

	mustRegister(t, o, 1, "service", 5)
	if err := o.BeginShutdown(10); err != nil {
		t.Fatalf("BeginShutdown() = %v", err)
	}

	const goroutines = 32
	var wg sync.WaitGroup
	results := make(chan ServiceState, goroutines)
	errs := make(chan error, goroutines)
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			state, err := o.Status("service", 20)
			if err != nil {
				errs <- err
				return
			}
			results <- state
		}()
	}
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent Status() = %v", err)
	}
	seen := 0
	for state := range results {
		if state.TerminationTime != 10 || state.StopTime != 15 || state.StopMethod != Killed {
			t.Fatalf("concurrent state = %+v, want kill at 15", state)
		}
		seen++
	}
	if seen != goroutines {
		t.Fatalf("got %d results, want %d", seen, goroutines)
	}
}

func TestReplayProducesSameTimetable(t *testing.T) {
	build := func() map[string]ServiceState {
		o, _ := testOrchestrator(t)
		mustRegister(t, o, 1, "leaf", 3)
		mustRegister(t, o, 2, "middle", 7, "leaf")
		mustRegister(t, o, 3, "root", 5, "middle")
		if err := o.BeginShutdown(10); err != nil {
			t.Fatalf("BeginShutdown() = %v", err)
		}
		if err := o.ReportExit("root", 12); err != nil {
			t.Fatalf("ReportExit(root) = %v", err)
		}
		states, err := o.Snapshot(30)
		if err != nil {
			t.Fatalf("Snapshot() = %v", err)
		}
		return states
	}

	first := build()
	second := build()
	if len(first) != len(second) {
		t.Fatalf("replayed state counts differ: %d vs %d", len(first), len(second))
	}
	for id, firstState := range first {
		if firstState != second[id] {
			t.Fatalf("service %q differs on replay: %+v vs %+v", id, firstState, second[id])
		}
	}
}
