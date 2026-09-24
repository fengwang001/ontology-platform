// Package sched runs a task DAG with bounded concurrency and failure modes.
package sched

import (
	"context"
	"sort"
	"sync"

	"ontology/exec"
	"ontology/fail"
	"ontology/graph"
	"ontology/report"
)

// Mode selects failure propagation semantics.
type Mode int

const (
	// FailFast cancels everything on the first failure (default).
	FailFast Mode = iota
	// BestEffort keeps failure-independent branches running.
	BestEffort
)

// Scheduler executes graphs; counters are unexported, read via accessors.
type Scheduler struct {
	maxConc     int
	mode        Mode
	peak        int
	readyChecks int
}

// New returns a Scheduler; maxConc < 1 degrades to serial execution.
func New(maxConc int, mode Mode) *Scheduler {
	if maxConc < 1 {
		maxConc = 1
	}
	return &Scheduler{maxConc: maxConc, mode: mode}
}

// Peak returns the historical max number of concurrently running tasks.
func (s *Scheduler) Peak() int { return s.peak }

// ReadyChecks returns the total number of readiness evaluations.
func (s *Scheduler) ReadyChecks() int { return s.readyChecks }

// Run executes the graph and returns a deterministic report. A cyclic
// graph is rejected before any task runs.
func (s *Scheduler) Run(g *graph.Graph, fns map[string]exec.Func) (*report.Report, error) {
	layers, err := g.Layers()
	if err != nil {
		return nil, err
	}
	s.peak, s.readyChecks = 0, 0
	tr := fail.NewTracker(g, layers)
	remaining := map[string]int{}
	var ready []string
	for _, id := range g.Tasks() {
		s.readyChecks++
		if remaining[id] = len(g.Parents(id)); remaining[id] == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan exec.Result, s.maxConc)
	var wg sync.WaitGroup
	running := map[string]bool{}
	halted := false

	for len(ready) > 0 || len(running) > 0 {
		for !halted && len(ready) > 0 && len(running) < s.maxConc {
			id := ready[0]
			ready = ready[1:]
			running[id] = true
			if len(running) > s.peak {
				s.peak = len(running)
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				results <- exec.Result{ID: id, Err: exec.Run(ctx, fns[id])}
			}()
		}
		if len(running) == 0 {
			break
		}
		res := <-results
		delete(running, res.ID)
		if tr.State(res.ID) == fail.Canceled {
			continue // late write-back from a canceled task: discard
		}
		if res.Err == nil {
			tr.Succeed(res.ID)
		} else {
			tr.Fail(res.ID, res.Err)
			if s.mode == FailFast && !halted {
				halted = true
				cancel()
				for id := range running {
					tr.Cancel(id)
				}
				tr.Halt(res.ID)
			}
		}
		for _, ch := range g.Children(res.ID) {
			s.readyChecks++
			remaining[ch]--
			if remaining[ch] == 0 && tr.State(ch) == fail.Pending {
				i := sort.SearchStrings(ready, ch)
				ready = append(ready, "")
				copy(ready[i+1:], ready[i:])
				ready[i] = ch
			}
		}
	}
	wg.Wait()
	rep := report.New()
	for _, id := range g.Tasks() {
		rep.Set(id, tr.State(id), tr.Reason(id))
	}
	return rep, nil
}
