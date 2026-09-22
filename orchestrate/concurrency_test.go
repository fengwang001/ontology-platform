package orchestrate

import (
	"context"
	"errors"
	"sync"
	"testing"

	"ontology/step"
)

// Queries issued during compensation must only ever observe a terminal
// phase, never a half-compensated mix.
func TestCompensationHiddenFromQueries(t *testing.T) {
	g := buildGraph(t, []string{"A", "B"}, [][2]string{{"A", "B"}})
	inComp := make(chan struct{})
	release := make(chan struct{})
	steps := []step.Step{
		{ID: "A", Run: func(context.Context) error { return nil },
			Compensate: func(context.Context) error {
				close(inComp)
				<-release
				return nil
			}},
		{ID: "B", Run: func(context.Context) error { return errors.New("B broke") }},
	}
	o, err := New(Config{Graph: g, Steps: steps})
	if err != nil {
		t.Fatal(err)
	}
	done := runAsync(o)
	<-inComp // compensation of A is in flight
	snapCh := make(chan Snapshot, 1)
	go func() { snapCh <- o.Snapshot() }()
	select {
	case snap := <-snapCh:
		t.Fatalf("query observed mid-compensation state: %+v", snap)
	default:
	}
	close(release)
	<-done
	snap := <-snapCh
	if snap.Phase != PhaseCompensated {
		t.Fatalf("phase = %s", snap.Phase)
	}
	if snap.Steps["A"].Status != step.Compensated {
		t.Fatalf("A = %s", snap.Steps["A"].Status)
	}
}

// Concurrent execution, queries and recovery must be race-free.
func TestConcurrentQueryAndRecover(t *testing.T) {
	g := buildGraph(t, []string{"a", "b", "c"},
		[][2]string{{"a", "b"}, {"a", "c"}})
	release := make(chan struct{})
	steps := []step.Step{
		{ID: "a", Run: func(ctx context.Context) error { <-release; return nil }},
		noop("b"), noop("c"),
	}
	o, err := New(Config{Graph: g, Steps: steps})
	if err != nil {
		t.Fatal(err)
	}
	done := runAsync(o)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				o.Snapshot()
				o.StepInfo("b")
				if _, _, err := Recover(Config{Graph: g, Steps: steps}, o.JournalBytes()); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	close(stop)
	wg.Wait()
	if o.Snapshot().Phase != PhaseCompleted {
		t.Fatalf("phase = %s", o.Snapshot().Phase)
	}
}
