package pipeline

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/dag"
	"ontology/rule"
)

// naive is an independent, deliberately simple model: after every
// operation it rescans the whole graph and evaluates every Created job
// whose needs are all decided, repeating until a fixpoint. The
// incremental cascade in Pipeline must agree with it on every step.
type naive struct {
	g       *dag.Graph
	specs   []JobSpec
	when    []rule.When
	state   []State
	retries []int
}

func newNaive(specs []JobSpec) *naive {
	names := make([]string, len(specs))
	needs := make([][]string, len(specs))
	n := &naive{specs: specs}
	for i, s := range specs {
		names[i] = s.Name
		needs[i] = s.Needs
		w, _ := rule.ParseWhen(s.When)
		n.when = append(n.when, w)
		n.state = append(n.state, Created)
		n.retries = append(n.retries, 0)
	}
	g, err := dag.Build(names, needs)
	if err != nil {
		panic(err)
	}
	n.g = g
	n.fixpoint()
	return n
}

func (n *naive) decided(i int) bool {
	s := n.state[i]
	return s.terminal() || (s == Manual && n.specs[i].AllowFailure)
}

func (n *naive) evalOne(i int) {
	var bad, skip bool
	for _, up := range n.g.Needs(i) {
		switch n.state[up] {
		case Failed:
			if !n.specs[up].AllowFailure {
				bad = true
			}
		case Skipped, Canceled:
			skip = true
		}
	}
	switch rule.Decide(n.when[i], bad, skip) {
	case rule.ToPending:
		n.state[i] = Pending
	case rule.ToManual:
		n.state[i] = Manual
	case rule.ToSkipped:
		n.state[i] = Skipped
	}
}

func (n *naive) fixpoint() {
	for changed := true; changed; {
		changed = false
		for i := range n.state {
			if n.state[i] != Created {
				continue
			}
			ready := true
			for _, up := range n.g.Needs(i) {
				if !n.decided(up) {
					ready = false
					break
				}
			}
			if ready {
				n.evalOne(i)
				changed = true
			}
		}
	}
}

func (n *naive) find(name string) (int, error) {
	if name == "" {
		return 0, ErrInvalidArgument
	}
	i, ok := n.g.Index(name)
	if !ok {
		return 0, ErrJobNotFound
	}
	return i, nil
}

func (n *naive) start(name string) error {
	i, err := n.find(name)
	if err != nil {
		return err
	}
	if n.state[i] != Pending {
		return ErrInvalidState
	}
	n.state[i] = Running
	return nil
}

func (n *naive) finish(name string, ok bool) error {
	i, err := n.find(name)
	if err != nil {
		return err
	}
	if n.state[i] != Running {
		return ErrInvalidState
	}
	if ok {
		n.state[i] = Success
		n.fixpoint()
		return nil
	}
	if n.retries[i] < n.specs[i].Retry {
		n.retries[i]++
		n.state[i] = Pending
		return nil
	}
	n.state[i] = Failed
	n.fixpoint()
	return nil
}

func (n *naive) play(name string) error {
	i, err := n.find(name)
	if err != nil {
		return err
	}
	if n.state[i] != Manual {
		return ErrInvalidState
	}
	n.state[i] = Pending
	return nil
}

func (n *naive) cancel() {
	for i := range n.state {
		if !n.state[i].terminal() {
			n.state[i] = Canceled
		}
	}
}

func (n *naive) retryJob(name string) error {
	i, err := n.find(name)
	if err != nil {
		return err
	}
	if n.state[i] != Failed && n.state[i] != Canceled {
		return ErrInvalidState
	}
	closure := n.g.Closure(i)
	for _, d := range closure {
		if n.state[d] == Running {
			return ErrInvalidState
		}
	}
	n.state[i] = Pending
	n.retries[i] = 0
	for _, d := range closure {
		n.state[d] = Created
		n.retries[d] = 0
	}
	n.fixpoint()
	return nil
}

func (n *naive) status() Status {
	var active, blocked, failed, canceled bool
	warnings := 0
	for i := range n.state {
		switch n.state[i] {
		case Pending, Running:
			active = true
		case Manual:
			if !n.specs[i].AllowFailure {
				blocked = true
			}
		case Failed:
			if n.specs[i].AllowFailure {
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

func errClass(err error) string {
	switch {
	case err == nil:
		return "nil"
	case errors.Is(err, ErrInvalidArgument):
		return "invalid-argument"
	case errors.Is(err, ErrJobNotFound):
		return "job-not-found"
	case errors.Is(err, ErrInvalidState):
		return "invalid-state"
	default:
		return "other"
	}
}

type randOp struct {
	kind string
	name string
	ok   bool
}

func (o randOp) String() string {
	if o.kind == "finish" {
		return fmt.Sprintf("finish(%s,%v)", o.name, o.ok)
	}
	return fmt.Sprintf("%s(%s)", o.kind, o.name)
}

func randomSpecs(r *rand.Rand) []JobSpec {
	n := 1 + r.Intn(10)
	whens := []string{"on_success", "on_failure", "always", "manual"}
	specs := make([]JobSpec, n)
	for i := 0; i < n; i++ {
		var needs []string
		for j := 0; j < i; j++ {
			if r.Intn(4) == 0 { // edges only to earlier jobs: acyclic
				needs = append(needs, fmt.Sprintf("j%d", j))
			}
		}
		specs[i] = JobSpec{
			Name:         fmt.Sprintf("j%d", i),
			Needs:        needs,
			When:         whens[r.Intn(len(whens))],
			AllowFailure: r.Intn(3) == 0,
			Retry:        r.Intn(3),
		}
	}
	return specs
}

func randomOps(r *rand.Rand, specs []JobSpec, count int) []randOp {
	kinds := []string{"start", "finish", "finish", "play", "retryjob", "retryjob", "cancel"}
	ops := make([]randOp, 0, count)
	for k := 0; k < count; k++ {
		kind := kinds[r.Intn(len(kinds))]
		name := specs[r.Intn(len(specs))].Name
		switch r.Intn(20) {
		case 0:
			name = "" // invalid argument path
		case 1:
			name = "ghost" // not-found path
		}
		ops = append(ops, randOp{kind: kind, name: name, ok: r.Intn(2) == 0})
	}
	return ops
}

func applyOp(p *Pipeline, n *naive, o randOp) (error, error) {
	switch o.kind {
	case "start":
		return p.Start(o.name), n.start(o.name)
	case "finish":
		return p.Finish(o.name, o.ok), n.finish(o.name, o.ok)
	case "play":
		return p.Play(o.name), n.play(o.name)
	case "retryjob":
		return p.RetryJob(o.name), n.retryJob(o.name)
	case "cancel":
		p.Cancel()
		n.cancel()
		return nil, nil
	}
	panic("bad op")
}

func TestRandomAgainstNaiveSimulation(t *testing.T) {
	const cases = 1500
	for seed := int64(0); seed < cases; seed++ {
		r := rand.New(rand.NewSource(seed))
		specs := randomSpecs(r)
		ops := randomOps(r, specs, 40)

		p, err := New(specs)
		if err != nil {
			t.Fatalf("seed=%d: New: %v", seed, err)
		}
		p2, err := New(specs) // replay twin: same op sequence, same result
		if err != nil {
			t.Fatalf("seed=%d: New twin: %v", seed, err)
		}
		n := newNaive(specs)

		fail := func(step int, o randOp, why string) {
			t.Fatalf("seed=%d step=%d op=%s: %s\nspecs=%+v\nops=%v\npipeline=%v\nnaive  =%v",
				seed, step, o, why, specs, ops[:step+1], dumpStates(p), n.state)
		}
		for step, o := range ops {
			errP, errN := applyOp(p, n, o)
			var errP2 error
			switch o.kind { // replay twin
			case "start":
				errP2 = p2.Start(o.name)
			case "finish":
				errP2 = p2.Finish(o.name, o.ok)
			case "play":
				errP2 = p2.Play(o.name)
			case "retryjob":
				errP2 = p2.RetryJob(o.name)
			case "cancel":
				p2.Cancel()
			}
			if errClass(errP) != errClass(errN) {
				fail(step, o, fmt.Sprintf("error class mismatch: pipeline=%v naive=%v", errP, errN))
			}
			if errClass(errP) != errClass(errP2) {
				fail(step, o, fmt.Sprintf("replay diverged: %v vs %v", errP, errP2))
			}
			for i := range specs {
				if p.jobs[i].state != n.state[i] || p.jobs[i].retriesUsed != n.retries[i] {
					fail(step, o, fmt.Sprintf("job %s: pipeline={%s r%d} naive={%s r%d}",
						specs[i].Name, p.jobs[i].state, p.jobs[i].retriesUsed, n.state[i], n.retries[i]))
				}
				if p2.jobs[i].state != p.jobs[i].state || p2.jobs[i].retriesUsed != p.jobs[i].retriesUsed {
					fail(step, o, fmt.Sprintf("replay twin diverged on job %s", specs[i].Name))
				}
			}
			if got, want := p.Status(), n.status(); got != want {
				fail(step, o, fmt.Sprintf("Status: pipeline=%+v naive=%+v", got, want))
			}
		}
		t.Logf("seed=%d jobs=%d ops=%d final=%+v states=%v (pipeline == naive fixpoint model)",
			seed, len(specs), len(ops), p.Status(), dumpStates(p))
	}
}

func dumpStates(p *Pipeline) []State {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]State, len(p.jobs))
	for i := range p.jobs {
		out[i] = p.jobs[i].state
	}
	return out
}

// TestConcurrent hammer the pipeline from many goroutines; with -race
// this proves the mutex makes concurrent calls equivalent to some
// serial order (no data races, no panics, invariants hold at the end).
func TestConcurrent(t *testing.T) {
	specs := []JobSpec{
		{Name: "a", When: "on_success", Retry: 2},
		{Name: "b", Needs: []string{"a"}, When: "on_success"},
		{Name: "c", Needs: []string{"a"}, When: "manual"},
		{Name: "d", Needs: []string{"b", "c"}, When: "always"},
		{Name: "e", Needs: []string{"d"}, When: "on_failure", AllowFailure: true},
	}
	p := mustNew(t, specs)
	names := []string{"a", "b", "c", "d", "e"}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for k := 0; k < 300; k++ {
				name := names[r.Intn(len(names))]
				switch r.Intn(6) {
				case 0:
					p.Start(name)
				case 1:
					p.Finish(name, r.Intn(2) == 0)
				case 2:
					p.Play(name)
				case 3:
					p.RetryJob(name)
				case 4:
					p.Status()
				case 5:
					p.StateOf(name)
				}
			}
		}(int64(g))
	}
	wg.Wait()
	st := p.Status()
	for i := range specs {
		// Invariant: a Created job always has at least one undecided need.
		if p.jobs[i].state != Created {
			continue
		}
		undecided := false
		for _, up := range p.g.Needs(i) {
			if !p.decided(up) {
				undecided = true
			}
		}
		if !undecided {
			t.Fatalf("job %s is Created but all needs decided", specs[i].Name)
		}
	}
	t.Logf("final status after concurrent hammering: %+v", st)
}
