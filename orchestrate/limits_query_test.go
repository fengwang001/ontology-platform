package orchestrate

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"ontology/journal"
	"ontology/step"
)

func TestLimitRejectionsAreDistinguishable(t *testing.T) {
	g := buildGraph(t, []string{"a", "b"}, [][2]string{{"a", "b"}})
	steps := []step.Step{noop("a"), noop("b")}

	_, err := New(Config{Graph: g, Steps: steps, Limits: Limits{MaxSteps: 1}})
	if !errors.Is(err, ErrTooManySteps) {
		t.Fatalf("want ErrTooManySteps, got %v", err)
	}
	_, err = New(Config{Graph: g, Steps: steps, Limits: Limits{MaxConcurrency: -1}})
	if !errors.Is(err, ErrConcurrencyLimit) {
		t.Fatalf("want ErrConcurrencyLimit, got %v", err)
	}
	_, err = New(Config{Graph: g, Steps: steps, Limits: Limits{MaxConcurrency: HardMaxConcurrency + 1}})
	if !errors.Is(err, ErrConcurrencyLimit) {
		t.Fatalf("want ErrConcurrencyLimit, got %v", err)
	}
	j := journal.New(0, nil)
	j.Append(journal.PhaseStart, "a", "")
	j.Append(journal.PhaseSuccess, "a", "")
	_, err = New(Config{Graph: g, Steps: steps, Journal: j, Limits: Limits{MaxJournalRecords: 2}})
	if !errors.Is(err, ErrJournalTooLong) {
		t.Fatalf("want ErrJournalTooLong, got %v", err)
	}
	// The three limit errors are mutually distinguishable.
	if errors.Is(ErrTooManySteps, ErrConcurrencyLimit) ||
		errors.Is(ErrConcurrencyLimit, ErrJournalTooLong) ||
		errors.Is(ErrTooManySteps, ErrJournalTooLong) {
		t.Fatal("limit errors must be distinguishable")
	}
}

func TestLimitRejectionChangesNothing(t *testing.T) {
	g := buildGraph(t, []string{"a", "b"}, [][2]string{{"a", "b"}})
	steps := []step.Step{noop("a"), noop("b")}
	j := journal.New(0, nil)
	j.Append(journal.PhaseStart, "a", "")
	j.Append(journal.PhaseSuccess, "a", "")
	before := j.Bytes()
	if _, err := New(Config{
		Graph: g, Steps: steps, Journal: j, Limits: Limits{MaxJournalRecords: 2},
	}); !errors.Is(err, ErrJournalTooLong) {
		t.Fatalf("want ErrJournalTooLong, got %v", err)
	}
	if !reflect.DeepEqual(j.Bytes(), before) {
		t.Fatal("rejected run mutated the journal")
	}
	// A journal-full error mid-run is also surfaced distinctly.
	o, err := New(Config{
		Graph: g, Steps: steps, Limits: Limits{MaxJournalRecords: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := o.Run(context.Background()); !errors.Is(err, ErrJournalTooLong) {
		t.Fatalf("want ErrJournalTooLong from run, got %v", err)
	}
}

func TestSnapshotIsStableAndNonAdvancing(t *testing.T) {
	g := buildGraph(t, []string{"a", "b"}, [][2]string{{"a", "b"}})
	release := make(chan struct{})
	started := make(chan struct{})
	steps := []step.Step{
		{ID: "a", Run: func(ctx context.Context) error {
			close(started)
			<-release
			return nil
		}},
		noop("b"),
	}
	o, err := New(Config{Graph: g, Steps: steps})
	if err != nil {
		t.Fatal(err)
	}
	done := runAsync(o)
	<-started
	s1 := o.Snapshot()
	for i := 0; i < 10; i++ { // queries must not advance any state
		if got := o.Snapshot(); !reflect.DeepEqual(got, s1) {
			t.Fatalf("snapshot drifted: %+v vs %+v", got, s1)
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	f1 := o.Snapshot()
	f2 := o.Snapshot()
	if !reflect.DeepEqual(f1, f2) {
		t.Fatal("terminal snapshots differ")
	}
	if f1.Phase != PhaseCompleted || f1.JournalLen != 4 {
		t.Fatalf("snapshot = %+v", f1)
	}
}

func TestUnregisteredStepQueryIsZeroValue(t *testing.T) {
	o, err := New(Config{
		Graph: buildGraph(t, []string{"a"}, nil),
		Steps: []step.Step{noop("a")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := o.StepInfo("nope"); got != (StepInfo{}) {
		t.Fatalf("unregistered step = %+v, want zero value", got)
	}
	if _, ok := o.Snapshot().Steps["nope"]; ok {
		t.Fatal("snapshot must not contain unregistered steps")
	}
}
