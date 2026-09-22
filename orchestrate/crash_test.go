package orchestrate

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"ontology/journal"
	"ontology/policy"
	"ontology/step"
)

// phasesIn decodes a journal snapshot and returns the set of step IDs
// that reached the given phase.
func phasesIn(t *testing.T, snapshot []byte, phase journal.Phase) map[string]bool {
	t.Helper()
	j, torn := journal.FromBytes(snapshot, 0, nil)
	if torn {
		t.Fatal("snapshot taken at a hook boundary must be well-formed")
	}
	out := map[string]bool{}
	for _, r := range j.Records() {
		if r.Phase == phase {
			out[r.StepID] = true
		}
	}
	return out
}

// countEvents measures how many hook events (before+after per record) a
// clean run produces, so the crash loop can enumerate every crash point.
func countEvents(t *testing.T, cfg Config) int {
	t.Helper()
	n := 0
	cfg.Hook = func(journal.Event) { n++ }
	o, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	o.Run(context.Background())
	return n
}

func crashOnce(t *testing.T, cfg Config, k int) (snap []byte, err error) {
	t.Helper()
	count := 0
	cfg.Hook = func(ev journal.Event) {
		if count == k {
			snap = ev.Snapshot()
			panic(CrashSignal{})
		}
		count++
	}
	o, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	runErr := o.Run(context.Background())
	if !errors.Is(runErr, ErrCrashed) {
		t.Fatalf("crash point %d: want ErrCrashed, got %v", k, runErr)
	}
	if snap == nil {
		t.Fatalf("crash point %d: hook never fired", k)
	}
	return snap, runErr
}

// Every crash point of a fully succeeding diamond DAG: recovery must not
// re-run any step whose Success is already journaled.
func TestAllCrashPointsSuccessRun(t *testing.T) {
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{{"A", "B"}, {"A", "C"}, {"B", "D"}, {"C", "D"}}
	g := buildGraph(t, nodes, edges)
	exec := map[string]*atomic.Int64{}
	var steps []step.Step
	for _, id := range nodes {
		exec[id] = &atomic.Int64{}
		steps = append(steps, step.Step{ID: id, Run: func(context.Context) error {
			exec[id].Add(1)
			return nil
		}})
	}
	total := countEvents(t, Config{Graph: g, Steps: steps})
	if total != 16 { // 4 steps x (Start+Success) x (before+after)
		t.Fatalf("events = %d, want 16", total)
	}
	for k := 0; k < total; k++ {
		for _, c := range exec {
			c.Store(0)
		}
		snap, _ := crashOnce(t, Config{Graph: g, Steps: steps}, k)
		before := map[string]int64{}
		for id, c := range exec {
			before[id] = c.Load()
		}
		o2, torn, err := Recover(Config{Graph: g, Steps: steps}, snap)
		if err != nil || torn {
			t.Fatalf("crash %d: recover err=%v torn=%v", k, err, torn)
		}
		if err := o2.Run(context.Background()); err != nil {
			t.Fatalf("crash %d: recovered run: %v", k, err)
		}
		if o2.Snapshot().Phase != PhaseCompleted {
			t.Fatalf("crash %d: phase = %s", k, o2.Snapshot().Phase)
		}
		for id := range phasesIn(t, snap, journal.PhaseSuccess) {
			if exec[id].Load() != before[id] {
				t.Fatalf("crash %d: completed step %s re-ran", k, id)
			}
		}
	}
}

// Every crash point of a failing chain (including compensation records):
// recovery must finish compensation exactly where the journal left off.
func TestAllCrashPointsFailingRun(t *testing.T) {
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{{"A", "B"}, {"B", "C"}}
	g := buildGraph(t, nodes, edges)
	comp := map[string]*atomic.Int64{"A": {}, "B": {}, "C": {}}
	mkSteps := func() []step.Step {
		var out []step.Step
		for _, id := range nodes {
			out = append(out, step.Step{
				ID: id,
				Run: func(context.Context) error {
					if id == "C" {
						return errors.New("C broke")
					}
					return nil
				},
				Compensate: func(context.Context) error {
					comp[id].Add(1)
					return nil
				},
			})
		}
		return out
	}
	cfg := Config{Graph: g, Steps: mkSteps(), Policy: policy.Policy{MaxRetries: 0}}
	total := countEvents(t, cfg)
	if total != 20 { // 10 records x (before+after)
		t.Fatalf("events = %d, want 20", total)
	}
	for k := 0; k < total; k++ {
		for _, c := range comp {
			c.Store(0)
		}
		snap, _ := crashOnce(t, Config{Graph: g, Steps: mkSteps()}, k)
		before := map[string]int64{}
		for id, c := range comp {
			before[id] = c.Load()
		}
		o2, _, err := Recover(Config{Graph: g, Steps: mkSteps()}, snap)
		if err != nil {
			t.Fatalf("crash %d: %v", k, err)
		}
		var ferr *FailureError
		if runErr := o2.Run(context.Background()); !errors.As(runErr, &ferr) {
			t.Fatalf("crash %d: want FailureError, got %v", k, runErr)
		}
		if o2.Snapshot().Phase != PhaseCompensated {
			t.Fatalf("crash %d: phase = %s", k, o2.Snapshot().Phase)
		}
		for id := range phasesIn(t, snap, journal.PhaseCompOK) {
			if comp[id].Load() != before[id] {
				t.Fatalf("crash %d: compensated step %s compensated again", k, id)
			}
		}
		if got := comp["C"].Load(); got != 0 {
			t.Fatalf("crash %d: failed step C compensated %d times", k, got)
		}
		if o2.StepInfo("A").Status != step.Compensated ||
			o2.StepInfo("B").Status != step.Compensated ||
			o2.StepInfo("C").Status != step.Failed {
			t.Fatalf("crash %d: bad final states: %+v", k, o2.Snapshot().Steps)
		}
	}
}
