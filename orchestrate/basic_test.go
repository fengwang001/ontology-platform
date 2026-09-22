package orchestrate

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"ontology/graph"
	"ontology/policy"
	"ontology/step"
)

func buildGraph(t *testing.T, nodes []string, edges [][2]string) *graph.Graph {
	t.Helper()
	g := graph.New()
	for _, n := range nodes {
		if err := g.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range edges {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			t.Fatal(err)
		}
	}
	return g
}

func noop(id string) step.Step {
	return step.Step{ID: id, Run: func(context.Context) error { return nil }}
}

func runAsync(o *Orchestrator) chan error {
	done := make(chan error, 1)
	go func() { done <- o.Run(context.Background()) }()
	return done
}

func TestWideDAGReachesFullLayerConcurrency(t *testing.T) {
	const width = 8
	nodes := []string{"root"}
	var edges [][2]string
	for i := 0; i < width; i++ {
		id := fmt.Sprintf("w%d", i)
		nodes = append(nodes, id)
		edges = append(edges, [2]string{"root", id})
	}
	started := make(chan string, width)
	release := make(chan struct{})
	steps := []step.Step{noop("root")}
	for _, id := range nodes[1:] {
		steps = append(steps, step.Step{ID: id, Run: func(ctx context.Context) error {
			started <- id
			<-release
			return nil
		}})
	}
	o, err := New(Config{Graph: buildGraph(t, nodes, edges), Steps: steps})
	if err != nil {
		t.Fatal(err)
	}
	done := runAsync(o)
	for i := 0; i < width; i++ {
		<-started
	}
	if got := o.MaxObservedConcurrency(); got != width {
		t.Fatalf("max concurrency = %d, want layer width %d", got, width)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := o.MaxObservedConcurrency(); got != width {
		t.Fatalf("max concurrency = %d, want %d", got, width)
	}
	if o.Snapshot().Phase != PhaseCompleted {
		t.Fatalf("phase = %s", o.Snapshot().Phase)
	}
}

func TestSlowStepDoesNotBlockSiblings(t *testing.T) {
	g := buildGraph(t, []string{"root", "slow", "fast1", "fast2"},
		[][2]string{{"root", "slow"}, {"root", "fast1"}, {"root", "fast2"}})
	release := make(chan struct{})
	fastDone := make(chan string, 2)
	slowStarted := make(chan struct{})
	steps := []step.Step{
		noop("root"),
		{ID: "slow", Run: func(ctx context.Context) error {
			close(slowStarted)
			<-release
			return nil
		}},
		{ID: "fast1", Run: func(ctx context.Context) error { fastDone <- "fast1"; return nil }},
		{ID: "fast2", Run: func(ctx context.Context) error { fastDone <- "fast2"; return nil }},
	}
	o, err := New(Config{Graph: g, Steps: steps})
	if err != nil {
		t.Fatal(err)
	}
	done := runAsync(o)
	<-slowStarted
	<-fastDone
	<-fastDone // both fast steps finished while slow is still blocked
	if s := o.StepInfo("fast1").Status; s != step.Succeeded {
		t.Fatalf("fast1 = %s", s)
	}
	if s := o.StepInfo("slow").Status; s != step.Running {
		t.Fatalf("slow = %s", s)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if o.Snapshot().Phase != PhaseCompleted {
		t.Fatalf("phase = %s", o.Snapshot().Phase)
	}
}

func TestRetryBoundaryIntegration(t *testing.T) {
	g := buildGraph(t, []string{"s"}, nil)
	exec := 0
	steps := []step.Step{{ID: "s", Run: func(context.Context) error {
		exec++
		return errors.New("transient")
	}}}
	clock := policy.NewFakeClock(time.Unix(0, 0))
	o, err := New(Config{
		Graph: g,
		Steps: steps,
		Policy: policy.Policy{
			MaxRetries: 3,
			Backoff:    func(attempt int) time.Duration { return time.Duration(attempt) * time.Second },
		},
		Clock: clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	var ferr *FailureError
	if err := o.Run(context.Background()); !errors.As(err, &ferr) {
		t.Fatalf("want FailureError, got %v", err)
	}
	if exec != 4 { // 3 retries => 4 total executions
		t.Fatalf("exec = %d, want 4", exec)
	}
	if got := o.StepInfo("s").ExecCount; got != 4 {
		t.Fatalf("journal exec count = %d, want 4", got)
	}
	if clock.Slept() != 6*time.Second {
		t.Fatalf("slept = %v, want 6s", clock.Slept())
	}
	if o.Snapshot().Phase != PhaseCompensated {
		t.Fatalf("phase = %s", o.Snapshot().Phase)
	}
}

func TestNonRetryableFailsImmediately(t *testing.T) {
	g := buildGraph(t, []string{"s"}, nil)
	exec := 0
	steps := []step.Step{{ID: "s", Run: func(context.Context) error {
		exec++
		return errors.New("fatal")
	}}}
	o, err := New(Config{
		Graph:  g,
		Steps:  steps,
		Policy: policy.Policy{MaxRetries: 5, Retryable: func(error) bool { return false }},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := o.Run(context.Background()); err == nil {
		t.Fatal("want failure")
	}
	if exec != 1 {
		t.Fatalf("exec = %d, want 1", exec)
	}
}
