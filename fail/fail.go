// Package fail orchestrates parallel graph execution and failure propagation.
package fail

import (
	"context"
	"errors"

	"ontology/exec"
	"ontology/graph"
	"ontology/report"
	"ontology/sched"
)

// Mode selects failure propagation semantics.
type Mode int

const (
	// FailFast cancels everything as soon as the first failure lands.
	FailFast Mode = iota + 1
	// BestEffort keeps running branches that do not depend on a failed task.
	BestEffort
)

// Options configures a run.
type Options struct {
	Limit int
	Mode  Mode
}

const (
	stPending = 0
	stRunning = 1
	stTerm    = 2
)

// Run validates the graph and executes all tasks concurrently up to Limit.
func Run(ctx context.Context, g *graph.Graph, funcs map[string]exec.TaskFunc, opts Options) (*report.Report, error) {
	if opts.Limit < 1 {
		opts.Limit = 1
	}
	for _, id := range g.Nodes() {
		if _, ok := funcs[id]; ok {
			continue
		}
		return nil, errors.New("fail: missing task function for node " + id)
	}
	if p := g.Cycle(); p != nil {
		return nil, &graph.CycleError{Path: p}
	}
	layers, err := g.Layers()
	if err != nil {
		return nil, err
	}
	layerOf := map[string]int{}
	for i, layer := range layers {
		for _, id := range layer {
			layerOf[id] = i
		}
	}

	indeg := map[string]int{}
	for _, id := range g.Nodes() {
		indeg[id] = len(g.Predecessors(id))
	}
	sc := sched.New(indeg, opts.Limit)
	ex := exec.New(len(g.Nodes()) + 1)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	state := map[string]int{}
	results := map[string]*report.TaskResult{}
	failureOrder := []string{}
	failed := map[string]bool{}
	aborted := false

	pump := func() {
		for {
			id, ok := sc.Next()
			if !ok {
				return
			}
			if state[id] != stPending {
				sc.Release(id, nil) // entry of a frozen (skipped) task
				continue
			}
			if opts.Mode == FailFast && aborted {
				state[id] = stTerm
				results[id] = &report.TaskResult{ID: id, State: report.Skipped}
				sc.Release(id, nil)
				continue
			}
			state[id] = stRunning
			ex.Run(id, funcs[id], runCtx)
		}
	}

	pump()
	for {
		ev := <-ex.Events
		id := ev.ID
		late := state[id] == stTerm
		if !late {
			switch ev.Outcome {
			case exec.Done:
				state[id] = stTerm
				results[id] = &report.TaskResult{ID: id, State: report.Succeeded}
			case exec.Failed, exec.Panicked:
				state[id] = stTerm
				failed[id] = true
				failureOrder = append(failureOrder, id)
				results[id] = &report.TaskResult{
					ID: id, State: report.Failed,
					Reason: &report.Reason{Err: ev.Err},
				}
				if opts.Mode == FailFast && !aborted {
					aborted = true
					cancel()
				}
			case exec.Canceled:
				state[id] = stTerm
				results[id] = &report.TaskResult{
					ID: id, State: report.Canceled,
					Reason: &report.Reason{Err: ev.Err},
				}
			}
		}
		if !late && ev.Outcome == exec.Done {
			sc.Release(id, g.Successors(id))
		} else {
			sc.Release(id, nil) // failure/cancel blocks successors; late write changes nothing
		}
		if opts.Mode == FailFast && aborted {
			freezeWaiting(state, results, sc)
		}
		pump()
		if sc.Active() == 0 {
			break
		}
	}

	fillRemaining(state, results, g, opts.Mode, failed, failureOrder, layerOf)
	return buildReport(g, results), nil
}

// freezeWaiting marks every never-launched task as skipped (cause filled later).
func freezeWaiting(state map[string]int, results map[string]*report.TaskResult, sc *sched.Scheduler) {
	for _, id := range sc.Waiting() {
		if state[id] == stPending {
			state[id] = stTerm
			results[id] = &report.TaskResult{ID: id, State: report.Skipped}
		}
	}
}

// fillRemaining computes deterministic skip roots with a topological DP and
// covers fail-fast leftovers that had no failed ancestor.
func fillRemaining(state map[string]int, results map[string]*report.TaskResult, g *graph.Graph,
	mode Mode, failed map[string]bool, order []string, layerOf map[string]int) {
	layers, _ := g.Layers()
	root := map[string]string{}
	for _, layer := range layers {
		for _, v := range layer {
			if failed[v] {
				root[v] = v
				continue
			}
			best := ""
			bestLayer := 1 << 30
			for _, p := range g.Predecessors(v) {
				rp, ok := root[p]
				if !ok {
					continue
				}
				if l := layerOf[rp]; l < bestLayer || (l == bestLayer && rp < best) {
					best, bestLayer = rp, l
				}
			}
			if best != "" {
				root[v] = best
			}
		}
	}
	first := ""
	for _, id := range order {
		if first == "" || layerOf[id] < layerOf[first] ||
			(layerOf[id] == layerOf[first] && id < first) {
			first = id
		}
	}
	for _, id := range g.Nodes() {
		if results[id] != nil && results[id].State == report.Skipped {
			if r, ok := root[id]; ok {
				results[id].Reason = &report.Reason{Kind: report.DependencyFailed, Cause: r}
			} else if mode == FailFast && first != "" {
				results[id].Reason = &report.Reason{Kind: report.Aborted, Cause: first}
			}
			state[id] = stTerm
		}
	}
}

func buildReport(g *graph.Graph, results map[string]*report.TaskResult) *report.Report {
	r := &report.Report{Results: make([]report.TaskResult, 0, len(g.Nodes()))}
	for _, id := range g.Nodes() {
		if x, ok := results[id]; ok {
			r.Results = append(r.Results, *x)
		} else {
			r.Results = append(r.Results, report.TaskResult{ID: id, State: report.Succeeded})
		}
	}
	return r
}
