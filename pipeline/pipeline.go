// Package pipeline implements a dependency-graph-driven job state
// machine. A single mutex guards all operations, so concurrent calls
// are equivalent to some serial order.
package pipeline

import (
	"errors"
	"fmt"
	"sync"

	"ontology/dag"
	"ontology/rule"
)

// Runtime rejection error classes, distinguishable with errors.Is.
var (
	ErrInvalidArgument = errors.New("pipeline: invalid argument")
	ErrJobNotFound     = errors.New("pipeline: job not found")
	ErrInvalidState    = errors.New("pipeline: invalid job state")
)

// State is a job state; Blocked only appears in pipeline-level Status.
type State int

const (
	Created State = iota
	Pending
	Running
	Manual
	Blocked
	Success
	Failed
	Skipped
	Canceled
)

func (s State) String() string {
	switch s {
	case Created:
		return "Created"
	case Pending:
		return "Pending"
	case Running:
		return "Running"
	case Manual:
		return "Manual"
	case Blocked:
		return "Blocked"
	case Success:
		return "Success"
	case Failed:
		return "Failed"
	case Skipped:
		return "Skipped"
	case Canceled:
		return "Canceled"
	}
	return "Unknown"
}

func (s State) terminal() bool {
	return s == Success || s == Failed || s == Skipped || s == Canceled
}

// JobSpec describes one job at construction time.
type JobSpec struct {
	Name         string
	Needs        []string
	When         string // on_success, on_failure, always, manual
	AllowFailure bool
	Retry        int // 0..2
}

// Status is the pipeline-level aggregate state.
type Status struct {
	State    State
	Warnings int // Failed jobs with allowFailure, reported on Success
}

type job struct {
	spec        JobSpec
	when        rule.When
	state       State
	retriesUsed int
}

// Pipeline is the job state machine.
type Pipeline struct {
	mu    sync.Mutex
	g     *dag.Graph
	jobs  []job
	evals int // evaluations performed; bounded by downstream closure size
}

// New validates the specs, builds the graph, and evaluates all
// need-less jobs (cascading to downstream jobs as they become ready).
// Validation reports only the first error class in the order: invalid
// argument, duplicate name, unknown dependency, cycle.
func New(specs []JobSpec) (*Pipeline, error) {
	if len(specs) < 1 || len(specs) > 10000 {
		return nil, fmt.Errorf("%w: job count %d out of range", ErrInvalidArgument, len(specs))
	}
	names := make([]string, len(specs))
	needs := make([][]string, len(specs))
	p := &Pipeline{jobs: make([]job, len(specs))}
	for i, s := range specs {
		w, ok := rule.ParseWhen(s.When)
		if s.Name == "" || !ok || s.Retry < 0 || s.Retry > 2 {
			return nil, fmt.Errorf("%w: job %q (when=%q retry=%d)", ErrInvalidArgument, s.Name, s.When, s.Retry)
		}
		names[i] = s.Name
		needs[i] = s.Needs
		p.jobs[i] = job{spec: s, when: w, state: Created}
	}
	g, err := dag.Build(names, needs)
	if err != nil {
		return nil, err
	}
	p.g = g
	var ready []int
	for i := range p.jobs {
		if len(g.Needs(i)) == 0 {
			p.evaluate(i)
			if p.decided(i) {
				ready = append(ready, i)
			}
		}
	}
	p.cascade(ready)
	return p, nil
}

// decided reports whether job i is settled as far as its downstream is
// concerned: terminal, or a non-blocking manual gate (Manual with
// allowFailure), which downstream treats as Success.
func (p *Pipeline) decided(i int) bool {
	j := &p.jobs[i]
	return j.state.terminal() || (j.state == Manual && j.spec.AllowFailure)
}

// evaluate runs the when-rule for a Created job whose needs are all
// decided. Each Created job is evaluated exactly once.
func (p *Pipeline) evaluate(i int) {
	p.evals++
	var bad, skip bool
	for _, n := range p.g.Needs(i) {
		nj := &p.jobs[n]
		switch nj.state {
		case Failed:
			if !nj.spec.AllowFailure {
				bad = true
			}
		case Skipped, Canceled:
			skip = true
		}
	}
	switch rule.Decide(p.jobs[i].when, bad, skip) {
	case rule.ToPending:
		p.jobs[i].state = Pending
	case rule.ToManual:
		p.jobs[i].state = Manual
	case rule.ToSkipped:
		p.jobs[i].state = Skipped
	}
}

// cascade evaluates Created downstream jobs whose needs have all become
// decided, starting from the just-decided jobs in queue.
func (p *Pipeline) cascade(queue []int) {
	for len(queue) > 0 {
		i := queue[0]
		queue = queue[1:]
		for _, d := range p.g.Downstream(i) {
			if p.jobs[d].state != Created {
				continue
			}
			ready := true
			for _, n := range p.g.Needs(d) {
				if !p.decided(n) {
					ready = false
					break
				}
			}
			if !ready {
				continue
			}
			p.evaluate(d)
			if p.decided(d) {
				queue = append(queue, d)
			}
		}
	}
}

// find resolves a job name, enforcing the rejection order:
// invalid argument (empty name) before job not found.
func (p *Pipeline) find(name string) (int, error) {
	if name == "" {
		return 0, ErrInvalidArgument
	}
	i, ok := p.g.Index(name)
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrJobNotFound, name)
	}
	return i, nil
}

// Start moves a Pending job to Running.
func (p *Pipeline) Start(name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	i, err := p.find(name)
	if err != nil {
		return err
	}
	if p.jobs[i].state != Pending {
		return fmt.Errorf("%w: %s is %s, want Pending", ErrInvalidState, name, p.jobs[i].state)
	}
	p.jobs[i].state = Running
	return nil
}

// Finish completes a Running job. ok=true yields Success. Otherwise the
// job returns to Pending while retries remain (without evaluating
// downstream), and becomes Failed once retries are exhausted.
func (p *Pipeline) Finish(name string, ok bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	i, err := p.find(name)
	if err != nil {
		return err
	}
	j := &p.jobs[i]
	if j.state != Running {
		return fmt.Errorf("%w: %s is %s, want Running", ErrInvalidState, name, j.state)
	}
	if ok {
		j.state = Success
		p.cascade([]int{i})
		return nil
	}
	if j.retriesUsed < j.spec.Retry {
		j.retriesUsed++
		j.state = Pending
		return nil
	}
	j.state = Failed
	p.cascade([]int{i})
	return nil
}

// Play triggers a job waiting at a manual gate: Manual to Pending.
// Downstream jobs already evaluated past a non-blocking gate are never
// revisited.
func (p *Pipeline) Play(name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	i, err := p.find(name)
	if err != nil {
		return err
	}
	if p.jobs[i].state != Manual {
		return fmt.Errorf("%w: %s is %s, want Manual", ErrInvalidState, name, p.jobs[i].state)
	}
	p.jobs[i].state = Pending
	return nil
}

// Cancel moves every non-terminal job (including Created and Manual) to
// Canceled. Nothing is evaluated afterwards.
func (p *Pipeline) Cancel() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.jobs {
		if !p.jobs[i].state.terminal() {
			p.jobs[i].state = Canceled
		}
	}
}

// RetryJob restarts a Failed or Canceled job: the target returns to
// Pending with retries reset, and its whole transitive downstream
// (whatever their states, including Success and Skipped) resets to
// Created to be re-evaluated. It is rejected if any downstream job is
// Running. Jobs outside the downstream closure are untouched.
func (p *Pipeline) RetryJob(name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	i, err := p.find(name)
	if err != nil {
		return err
	}
	if st := p.jobs[i].state; st != Failed && st != Canceled {
		return fmt.Errorf("%w: %s is %s, want Failed or Canceled", ErrInvalidState, name, st)
	}
	closure := p.g.Closure(i)
	for _, d := range closure {
		if p.jobs[d].state == Running {
			return fmt.Errorf("%w: downstream %s is Running", ErrInvalidState, p.g.Name(d))
		}
	}
	p.jobs[i].state = Pending
	p.jobs[i].retriesUsed = 0
	for _, d := range closure {
		p.jobs[d].state = Created
		p.jobs[d].retriesUsed = 0
	}
	return nil
}

// Status aggregates the pipeline state, first match wins:
// Running (any Running or Pending), Blocked (any blocking manual gate),
// Failed (any Failed without allowFailure), Canceled, else Success with
// Warnings counting Failed jobs with allowFailure.
func (p *Pipeline) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	var active, blocked, failed, canceled bool
	warnings := 0
	for i := range p.jobs {
		j := &p.jobs[i]
		switch j.state {
		case Pending, Running:
			active = true
		case Manual:
			if !j.spec.AllowFailure {
				blocked = true
			}
		case Failed:
			if j.spec.AllowFailure {
				warnings++
			} else {
				failed = true
			}
		case Canceled:
			canceled = true
		}
	}
	switch {
	case active:
		return Status{State: Running}
	case blocked:
		return Status{State: Blocked}
	case failed:
		return Status{State: Failed}
	case canceled:
		return Status{State: Canceled}
	default:
		return Status{State: Success, Warnings: warnings}
	}
}

// StateOf returns the current state of the named job.
func (p *Pipeline) StateOf(name string) (State, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	i, err := p.find(name)
	if err != nil {
		return Created, err
	}
	return p.jobs[i].state, nil
}
