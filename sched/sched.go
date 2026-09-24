// Package sched runs a task DAG with bounded concurrency, propagating
// failures either fail-fast (default) or best-effort.
package sched

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"ontology/exec"
	"ontology/fail"
	"ontology/graph"
	"ontology/report"
)

// TaskFunc is the executable body of a task.
type TaskFunc func(context.Context) error

// Options configures a run.
type Options struct {
	// MaxConcurrency caps simultaneously running tasks; <= 0 means 1.
	MaxConcurrency int
	// BestEffort keeps unrelated branches running after a failure instead
	// of aborting the whole run.
	BestEffort bool
}

// Stats exposes scheduler counters collected during runs.
type Stats struct {
	// MaxConcurrent is the historical peak of simultaneously running tasks.
	MaxConcurrent int
	// ReadyChecks is the number of readiness evaluations performed.
	ReadyChecks int
}

// Scheduler executes task graphs and accumulates counters across runs.
type Scheduler struct {
	maxConcurrent int
	readyChecks   int
}

// Stats returns the counters accumulated so far.
func (s *Scheduler) Stats() Stats {
	return Stats{MaxConcurrent: s.maxConcurrent, ReadyChecks: s.readyChecks}
}

type outcome struct {
	id        string
	res       exec.Result
	cancelled bool // ctx was already cancelled when the task returned
}

// Run executes every task in g exactly once, honouring dependencies, and
// returns the final report. It fails before running anything if the graph
// has a cycle or a task function is missing.
func (s *Scheduler) Run(g *graph.Graph, tasks map[string]TaskFunc, opts Options) (*report.Report, error) {
	if path := g.FindCycle(); len(path) > 0 {
		return nil, &graph.CycleError{Path: path}
	}
	nodes := g.Nodes()
	for _, id := range nodes {
		if tasks[id] == nil {
			return nil, fmt.Errorf("sched: missing task function for %q", id)
		}
	}
	limit := max(opts.MaxConcurrency, 1)
	rep := report.New()
	remaining := make(map[string]int, len(nodes))
	skipped := make(map[string]bool, len(nodes))
	inflight := make(map[string]bool, limit)
	var ready []string // kept sorted ascending
	s.readyChecks += len(nodes)
	for _, id := range nodes {
		remaining[id] = len(g.Dependencies(id))
		if remaining[id] == 0 {
			ready = insertSorted(ready, id)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan outcome, limit)
	running := 0
	aborted := false
	firstFail := ""

	var markSkipped func(id, reason string)
	markSkipped = func(id, reason string) {
		if skipped[id] || inflight[id] || rep.Has(id) {
			return
		}
		skipped[id] = true
		rep.Set(id, report.Entry{Status: fail.Skipped, Reason: reason, Err: fail.ErrSkipped})
		for _, next := range g.Dependents(id) {
			markSkipped(next, reason)
		}
	}
	onFailure := func(id string) {
		if !opts.BestEffort && !aborted {
			aborted, firstFail = true, id
			cancel()
			for _, n := range nodes {
				markSkipped(n, id)
			}
		}
		for _, next := range g.Dependents(id) {
			markSkipped(next, id)
		}
	}

	for running > 0 || len(ready) > 0 {
		for len(ready) > 0 && running < limit && !aborted {
			id := ready[0]
			ready = ready[1:]
			if skipped[id] || rep.Has(id) {
				continue
			}
			inflight[id] = true
			running++
			if running > s.maxConcurrent {
				s.maxConcurrent = running
			}
			go func() {
				res := exec.Run(ctx, tasks[id])
				done <- outcome{id, res, ctx.Err() != nil}
			}()
		}
		if running == 0 {
			break
		}
		o := <-done
		running--
		delete(inflight, o.id)
		switch {
		case o.res.Panicked:
			rep.Set(o.id, report.Entry{Status: fail.Failed, Reason: o.id,
				Err: &fail.PanicError{Value: o.res.Panic}})
			onFailure(o.id)
		case o.res.Err != nil && !errors.Is(o.res.Err, context.Canceled):
			rep.Set(o.id, report.Entry{Status: fail.Failed, Reason: o.id, Err: o.res.Err})
			onFailure(o.id)
		case o.cancelled:
			rep.Set(o.id, report.Entry{Status: fail.Cancelled, Reason: firstFail,
				Err: fail.ErrCancelled})
		default:
			rep.Set(o.id, report.Entry{Status: fail.Success})
			for _, next := range g.Dependents(o.id) {
				s.readyChecks++
				remaining[next]--
				if remaining[next] == 0 && !skipped[next] {
					ready = insertSorted(ready, next)
				}
			}
		}
	}
	return rep, nil
}

func insertSorted(s []string, id string) []string {
	i, _ := slices.BinarySearch(s, id)
	return slices.Insert(s, i, id)
}
