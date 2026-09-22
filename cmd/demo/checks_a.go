package main

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"ontology/graph"
	"ontology/orchestrate"
	"ontology/step"
)

func checkLayeredConcurrency() bool {
	const width = 6
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
		steps = append(steps, step.Step{ID: id, Run: func(context.Context) error {
			started <- id
			<-release
			return nil
		}})
	}
	o, err := orchestrate.New(orchestrate.Config{Graph: buildGraph(nodes, edges), Steps: steps})
	if err != nil {
		return false
	}
	done := make(chan error, 1)
	go func() { done <- o.Run(context.Background()) }()
	for i := 0; i < width; i++ {
		<-started
	}
	close(release)
	return <-done == nil && o.MaxObservedConcurrency() == width
}

func checkCyclePath() bool {
	g := buildGraph([]string{"a", "b", "c"}, [][2]string{{"a", "b"}, {"b", "c"}, {"c", "a"}})
	var ce *graph.CycleError
	if !errors.As(g.Validate(), &ce) {
		return false
	}
	p := ce.Path
	if len(p) < 2 || p[0] != p[len(p)-1] {
		return false
	}
	real := map[[2]string]bool{{"a", "b"}: true, {"b", "c"}: true, {"c", "a"}: true}
	for i := 0; i+1 < len(p); i++ {
		if !real[[2]string{p[i], p[i+1]}] {
			return false
		}
	}
	return true
}

// chainRun executes A->B->C->D with C failing; compFail optionally makes
// one compensation fail. It returns the orchestrator and the ordered
// compensation events.
func chainRun(compFail string) (*orchestrate.Orchestrator, []string) {
	g := buildGraph([]string{"A", "B", "C", "D"},
		[][2]string{{"A", "B"}, {"B", "C"}, {"C", "D"}})
	var mu sync.Mutex
	var events []string
	mk := func(id string) step.Step {
		return step.Step{
			ID: id,
			Run: func(context.Context) error {
				if id == "C" {
					return errors.New("C broke")
				}
				return nil
			},
			Compensate: func(context.Context) error {
				mu.Lock()
				events = append(events, id+":start")
				mu.Unlock()
				defer func() {
					mu.Lock()
					events = append(events, id+":done")
					mu.Unlock()
				}()
				if id == compFail {
					return errors.New("comp broke")
				}
				return nil
			},
		}
	}
	o, err := orchestrate.New(orchestrate.Config{
		Graph: g,
		Steps: []step.Step{mk("A"), mk("B"), mk("C"), mk("D")},
	})
	if err != nil {
		return nil, nil
	}
	o.Run(context.Background())
	mu.Lock()
	defer mu.Unlock()
	return o, append([]string{}, events...)
}

func indexOf(events []string, want string) int {
	for i, e := range events {
		if e == want {
			return i
		}
	}
	return -1
}

func checkReverseCompensation() bool {
	o, events := chainRun("")
	if o == nil || o.Snapshot().Phase != orchestrate.PhaseCompensated {
		return false
	}
	bDone, aStart := indexOf(events, "B:done"), indexOf(events, "A:start")
	return bDone >= 0 && aStart >= 0 && bDone < aStart
}

func checkCompensateOnce() bool {
	o, _ := chainRun("")
	if o == nil {
		return false
	}
	s := o.Snapshot().Steps
	return s["A"].CompCount == 1 && s["B"].CompCount == 1 &&
		s["C"].CompCount == 0 && s["C"].ExecCount == 1 &&
		s["D"].CompCount == 0 && s["D"].ExecCount == 0
}

func checkCompensationAggregation() bool {
	o, events := chainRun("B")
	if o == nil {
		return false
	}
	s := o.Snapshot().Steps
	return s["B"].Status == step.CompensateFailed &&
		s["A"].Status == step.Compensated && // A still compensated after B failed
		indexOf(events, "A:done") >= 0 &&
		o.Snapshot().Phase == orchestrate.PhaseCompensated
}
