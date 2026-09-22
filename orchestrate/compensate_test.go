package orchestrate

import (
	"context"
	"errors"
	"sync"
	"testing"

	"ontology/policy"
	"ontology/step"
)

// eventLog records compensation events from step closures.
type eventLog struct {
	mu  sync.Mutex
	log []string
}

func (e *eventLog) add(s string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.log = append(e.log, s)
}

func (e *eventLog) index(s string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i, v := range e.log {
		if v == s {
			return i
		}
	}
	return -1
}

// chainFixture builds A->B->C->D where C fails terminally and D never
// starts. compFail, if non-empty, is the step whose compensation fails.
func chainFixture(t *testing.T, ev *eventLog, compFail string) *Orchestrator {
	t.Helper()
	g := buildGraph(t, []string{"A", "B", "C", "D"},
		[][2]string{{"A", "B"}, {"B", "C"}, {"C", "D"}})
	exec := func(id string) func(context.Context) error {
		return func(context.Context) error {
			if id == "C" {
				return errors.New("C broke")
			}
			return nil
		}
	}
	comp := func(id string) func(context.Context) error {
		return func(context.Context) error {
			ev.add(id + ":start")
			defer ev.add(id + ":done")
			if id == compFail {
				return errors.New(id + " compensation broke")
			}
			return nil
		}
	}
	var steps []step.Step
	for _, id := range []string{"A", "B", "C", "D"} {
		steps = append(steps, step.Step{ID: id, Run: exec(id), Compensate: comp(id)})
	}
	o, err := New(Config{Graph: g, Steps: steps, Policy: policy.Policy{MaxRetries: 2}})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestReverseTopologicalCompensation(t *testing.T) {
	var ev eventLog
	o := chainFixture(t, &ev, "")
	if err := o.Run(context.Background()); err == nil {
		t.Fatal("want failure")
	}
	// B's compensation must fully complete before A's starts.
	if b, a := ev.index("B:done"), ev.index("A:start"); b < 0 || a < 0 || b > a {
		t.Fatalf("events = %v", ev.log)
	}
	snap := o.Snapshot()
	if snap.Phase != PhaseCompensated {
		t.Fatalf("phase = %s", snap.Phase)
	}
	want := map[string]StepInfo{
		"A": {Status: step.Compensated, ExecCount: 1, CompCount: 1},
		"B": {Status: step.Compensated, ExecCount: 1, CompCount: 1},
		"C": {Status: step.Failed, ExecCount: 3, CompCount: 0}, // 2 retries
		"D": {Status: step.Pending, ExecCount: 0, CompCount: 0},
	}
	for id, w := range want {
		if got := snap.Steps[id]; got != w {
			t.Fatalf("%s = %+v, want %+v", id, got, w)
		}
	}
}

func TestCompensationFailureAggregatesAndContinues(t *testing.T) {
	var ev eventLog
	o := chainFixture(t, &ev, "B")
	err := o.Run(context.Background())
	var ferr *FailureError
	if !errors.As(err, &ferr) {
		t.Fatalf("want FailureError, got %v", err)
	}
	if ferr.Step != "C" {
		t.Fatalf("failed step = %s", ferr.Step)
	}
	var cerr *CompensationError
	if !errors.As(ferr.Compensation, &cerr) {
		t.Fatalf("want CompensationError, got %v", ferr.Compensation)
	}
	if len(cerr.Failures) != 1 || cerr.Failures["B"] == nil {
		t.Fatalf("failures = %v", cerr.Failures)
	}
	// A's compensation still ran after B's failed.
	if ev.index("A:done") < 0 || ev.index("B:done") < 0 {
		t.Fatalf("events = %v", ev.log)
	}
	if ev.index("B:done") > ev.index("A:start") {
		t.Fatalf("B must be compensated before A: %v", ev.log)
	}
	snap := o.Snapshot()
	if snap.Steps["A"].Status != step.Compensated {
		t.Fatalf("A = %s", snap.Steps["A"].Status)
	}
	if snap.Steps["B"].Status != step.CompensateFailed {
		t.Fatalf("B = %s", snap.Steps["B"].Status)
	}
	if snap.Phase != PhaseCompensated {
		t.Fatalf("phase = %s", snap.Phase)
	}
}

func TestCompensationRetriedPerPolicy(t *testing.T) {
	g := buildGraph(t, []string{"A", "B"}, [][2]string{{"A", "B"}})
	compCalls := 0
	steps := []step.Step{
		{ID: "A", Run: func(context.Context) error { return nil },
			Compensate: func(context.Context) error {
				compCalls++
				if compCalls < 3 {
					return errors.New("comp transient")
				}
				return nil
			}},
		{ID: "B", Run: func(context.Context) error { return errors.New("B broke") }},
	}
	o, err := New(Config{
		Graph:  g,
		Steps:  steps,
		Policy: policy.Policy{CompensateRetries: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	var ferr *FailureError
	if runErr := o.Run(context.Background()); !errors.As(runErr, &ferr) {
		t.Fatalf("want FailureError, got %v", runErr)
	} else if ferr.Compensation != nil {
		t.Fatalf("compensation should succeed after retries: %v", ferr.Compensation)
	}
	if compCalls != 3 {
		t.Fatalf("comp calls = %d, want 3", compCalls)
	}
	if got := o.StepInfo("A").CompCount; got != 3 {
		t.Fatalf("journal comp count = %d, want 3", got)
	}
}
