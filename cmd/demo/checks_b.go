package main

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"

	"ontology/journal"
	"ontology/orchestrate"
	"ontology/policy"
	"ontology/step"
)

func checkCrashPoints() bool {
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{{"A", "B"}, {"A", "C"}}
	g := buildGraph(nodes, edges)
	exec := map[string]*atomic.Int64{}
	var steps []step.Step
	for _, id := range nodes {
		exec[id] = &atomic.Int64{}
		steps = append(steps, step.Step{ID: id, Run: func(context.Context) error {
			exec[id].Add(1)
			return nil
		}})
	}
	total := 0
	probe, err := orchestrate.New(orchestrate.Config{
		Graph: g, Steps: steps,
		Hook: func(journal.Event) { total++ },
	})
	if err != nil || probe.Run(context.Background()) != nil || total == 0 {
		return false
	}
	for k := 0; k < total; k++ {
		for _, c := range exec {
			c.Store(0)
		}
		count := 0
		var snap []byte
		cfg := orchestrate.Config{
			Graph: g,
			Steps: steps,
			Hook: func(ev journal.Event) {
				if count == k {
					snap = ev.Snapshot()
					panic(orchestrate.CrashSignal{})
				}
				count++
			},
		}
		o, err := orchestrate.New(cfg)
		if err != nil {
			return false
		}
		if runErr := o.Run(context.Background()); !errors.Is(runErr, orchestrate.ErrCrashed) {
			return false
		}
		before := map[string]int64{}
		for id, c := range exec {
			before[id] = c.Load()
		}
		o2, torn, err := orchestrate.Recover(orchestrate.Config{Graph: g, Steps: steps}, snap)
		if err != nil || torn {
			return false
		}
		if err := o2.Run(context.Background()); err != nil {
			return false
		}
		if o2.Snapshot().Phase != orchestrate.PhaseCompleted {
			return false
		}
		j, _ := journal.FromBytes(snap, 0, nil)
		for _, r := range j.Records() {
			if r.Phase == journal.PhaseSuccess && exec[r.StepID].Load() != before[r.StepID] {
				return false // a completed step re-ran after recovery
			}
		}
	}
	return true
}

func flakyChainJournal(fails int) ([]byte, bool) {
	g := buildGraph([]string{"a", "b"}, [][2]string{{"a", "b"}})
	n := 0
	steps := []step.Step{
		{ID: "a", Run: func(context.Context) error {
			if n < fails {
				n++
				return errors.New("transient")
			}
			return nil
		}},
		noop("b"),
	}
	o, err := orchestrate.New(orchestrate.Config{
		Graph: g, Steps: steps, Policy: policy.Policy{MaxRetries: fails},
	})
	if err != nil || o.Run(context.Background()) != nil {
		return nil, false
	}
	return o.JournalBytes(), true
}

func checkReplayIdempotent() bool {
	bytes, ok := flakyChainJournal(7)
	if !ok {
		return false
	}
	mk := func() (orchestrate.Snapshot, bool) {
		o, torn, err := orchestrate.Recover(orchestrate.Config{
			Graph: buildGraph([]string{"a", "b"}, [][2]string{{"a", "b"}}),
			Steps: []step.Step{noop("a"), noop("b")},
		}, bytes)
		if err != nil || torn {
			return orchestrate.Snapshot{}, false
		}
		return o.Snapshot(), true
	}
	s1, ok1 := mk()
	s2, ok2 := mk()
	return ok1 && ok2 && reflect.DeepEqual(s1, s2) && s1.Steps["a"].ExecCount == 8
}

func checkTornRecord() bool {
	g := buildGraph([]string{"a", "b"}, [][2]string{{"a", "b"}})
	o, err := orchestrate.New(orchestrate.Config{Graph: g, Steps: []step.Step{noop("a"), noop("b")}})
	if err != nil || o.Run(context.Background()) != nil {
		return false
	}
	full := o.JournalBytes()
	torn := full[:len(full)-3] // crash halfway through the last record
	reruns := 0
	steps := []step.Step{
		{ID: "a", Run: func(context.Context) error { reruns++; return nil }},
		{ID: "b", Run: func(context.Context) error { reruns++; return nil }},
	}
	o2, wasTorn, err := orchestrate.Recover(orchestrate.Config{Graph: g, Steps: steps}, torn)
	if err != nil || !wasTorn {
		return false
	}
	if err := o2.Run(context.Background()); err != nil {
		return false
	}
	return o2.Snapshot().Phase == orchestrate.PhaseCompleted && reruns == 1
}

func checkRetryBoundary() bool {
	g := buildGraph([]string{"s"}, nil)
	exec := 0
	steps := []step.Step{{ID: "s", Run: func(context.Context) error {
		exec++
		return errors.New("transient")
	}}}
	o, err := orchestrate.New(orchestrate.Config{
		Graph: g, Steps: steps, Policy: policy.Policy{MaxRetries: 3},
	})
	if err != nil {
		return false
	}
	runErr := o.Run(context.Background())
	var ferr *orchestrate.FailureError
	return errors.As(runErr, &ferr) && exec == 4 && o.StepInfo("s").ExecCount == 4
}

func checkReplayComplexity() bool {
	small, ok1 := flakyChainJournal(96)   // 100 records
	large, ok2 := flakyChainJournal(9996) // 10000 records
	if !ok1 || !ok2 {
		return false
	}
	visits := func(b []byte) (int, int, bool) {
		o, torn, err := orchestrate.Recover(orchestrate.Config{
			Graph: buildGraph([]string{"a", "b"}, [][2]string{{"a", "b"}}),
			Steps: []step.Step{noop("a"), noop("b")},
		}, b)
		if err != nil || torn {
			return 0, 0, false
		}
		return o.ReplayNodeVisits(), o.Snapshot().JournalLen, true
	}
	v1, l1, ok1 := visits(small)
	v2, l2, ok2 := visits(large)
	return ok1 && ok2 && l1 >= 100 && l2 >= 10000 && v1 == v2
}
