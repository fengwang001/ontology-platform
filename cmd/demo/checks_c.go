package main

import (
	"context"
	"errors"
	"reflect"

	"ontology/journal"
	"ontology/orchestrate"
	"ontology/step"
)

func checkLimits() bool {
	g := buildGraph([]string{"a", "b"}, [][2]string{{"a", "b"}})
	steps := []step.Step{noop("a"), noop("b")}
	_, err1 := orchestrate.New(orchestrate.Config{
		Graph: g, Steps: steps, Limits: orchestrate.Limits{MaxSteps: 1},
	})
	_, err2 := orchestrate.New(orchestrate.Config{
		Graph: g, Steps: steps, Limits: orchestrate.Limits{MaxConcurrency: -1},
	})
	j := journal.New(0, nil)
	j.Append(journal.PhaseStart, "a", "")
	j.Append(journal.PhaseSuccess, "a", "")
	before := j.Bytes()
	_, err3 := orchestrate.New(orchestrate.Config{
		Graph: g, Steps: steps, Journal: j, Limits: orchestrate.Limits{MaxJournalRecords: 2},
	})
	return errors.Is(err1, orchestrate.ErrTooManySteps) &&
		errors.Is(err2, orchestrate.ErrConcurrencyLimit) &&
		errors.Is(err3, orchestrate.ErrJournalTooLong) &&
		!errors.Is(err1, orchestrate.ErrConcurrencyLimit) &&
		!errors.Is(err2, orchestrate.ErrJournalTooLong) &&
		!errors.Is(err3, orchestrate.ErrTooManySteps) &&
		reflect.DeepEqual(j.Bytes(), before) // rejection changed nothing
}

func checkQueryStability() bool {
	g := buildGraph([]string{"a", "b"}, [][2]string{{"a", "b"}})
	release := make(chan struct{})
	started := make(chan struct{})
	steps := []step.Step{
		{ID: "a", Run: func(context.Context) error {
			close(started)
			<-release
			return nil
		}},
		noop("b"),
	}
	o, err := orchestrate.New(orchestrate.Config{Graph: g, Steps: steps})
	if err != nil {
		return false
	}
	done := make(chan error, 1)
	go func() { done <- o.Run(context.Background()) }()
	<-started
	s1 := o.Snapshot()
	stable := true
	for i := 0; i < 10; i++ {
		if !reflect.DeepEqual(o.Snapshot(), s1) {
			stable = false
		}
	}
	close(release)
	if <-done != nil {
		return false
	}
	f1, f2 := o.Snapshot(), o.Snapshot()
	return stable && reflect.DeepEqual(f1, f2) &&
		f1.Phase == orchestrate.PhaseCompleted &&
		o.StepInfo("unregistered") == (orchestrate.StepInfo{})
}

func checkSlowStep() bool {
	g := buildGraph([]string{"root", "slow", "fast"},
		[][2]string{{"root", "slow"}, {"root", "fast"}})
	release := make(chan struct{})
	slowStarted := make(chan struct{})
	fastDone := make(chan struct{})
	steps := []step.Step{
		noop("root"),
		{ID: "slow", Run: func(context.Context) error {
			close(slowStarted)
			<-release
			return nil
		}},
		{ID: "fast", Run: func(context.Context) error {
			close(fastDone)
			return nil
		}},
	}
	o, err := orchestrate.New(orchestrate.Config{Graph: g, Steps: steps})
	if err != nil {
		return false
	}
	done := make(chan error, 1)
	go func() { done <- o.Run(context.Background()) }()
	<-slowStarted
	<-fastDone // fast finished while slow is still blocked
	fastSucceeded := o.StepInfo("fast").Status == step.Succeeded &&
		o.StepInfo("slow").Status == step.Running
	close(release)
	return fastSucceeded && <-done == nil &&
		o.Snapshot().Phase == orchestrate.PhaseCompleted
}
