package orchestrate

import (
	"sync"
	"testing"
	"time"
)

func TestWideDAGMaxConcurrency(t *testing.T) {
	const w = 8
	g := wideDAG(w)
	c := newCounters()
	c.enableGate()
	o, err := New(g, Config{}, actionsFor(g, c))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _, _ = o.Run(); close(done) }()
	deadline := time.After(2 * time.Second)
	for o.MaxObservedConcurrency() < w {
		select {
		case <-deadline:
			t.Fatalf("never reached width, peak %d", o.MaxObservedConcurrency())
		case <-time.After(time.Millisecond):
		}
	}
	c.releaseGate()
	<-done
	snap := o.Query()
	if snap.Phase != PhaseCompleted {
		t.Fatalf("phase %s", snap.Phase)
	}
	if got := o.MaxObservedConcurrency(); got != w {
		t.Fatalf("max concurrency %d, want %d", got, w)
	}
	if got := o.MaxObservedConcurrency(); got != w {
		t.Fatalf("max concurrency %d, want %d", got, w)
	}
	for _, sv := range snap.Steps {
		if sv.ExecCount != 1 {
			t.Fatalf("%s exec %d", sv.ID, sv.ExecCount)
		}
	}
}

func TestSlowStepDoesNotBlockLayer(t *testing.T) {
	g := wideDAG(3)
	c := newCounters()
	c.hold("n0")
	o, _ := New(g, Config{}, actionsFor(g, c))
	done := make(chan Snapshot)
	go func() {
		s, _ := o.Run()
		done <- s
	}()
	c.waitStarted("n0")
	// While n0 is blocked, n1 and n2 must finish.
	deadline := time.After(2 * time.Second)
	for _, id := range []string{"n1", "n2"} {
		for {
			e, _ := c.counts(id)
			if e == 1 {
				break
			}
			select {
			case <-deadline:
				t.Fatalf("%s blocked by slow peer", id)
			default:
			}
			time.Sleep(time.Millisecond)
		}
	}
	c.releaseStep("n0")
	<-done
}

func TestCompensationOrderBbeforeA(t *testing.T) {
	g := chainABC()
	c := newCounters()
	c.failExec["C"] = 1 // C fails terminally
	var orderMu sync.Mutex
	var order []string
	// Wrap compensations to record completion order (after action returns).
	acts := actionsFor(g, c)
	for _, id := range []string{"A", "B"} {
		id := id
		act := acts[id]
		baseComp := act.Compensate
		act.Compensate = func() error {
			err := baseComp()
			orderMu.Lock()
			order = append(order, id)
			orderMu.Unlock()
			return err
		}
		acts[id] = act
	}
	o, _ := New(g, Config{}, acts)
	snap, err := o.Run()
	if err != nil {
		t.Fatal(err)
	}
	if snap.Phase != PhaseAborted {
		t.Fatalf("phase %s", snap.Phase)
	}
	if len(order) != 2 || order[0] != "B" || order[1] != "A" {
		t.Fatalf("compensation completion order %v, want [B A]", order)
	}
	// Exactly once: C failed (not compensated), A/B compensated once.
	if e, cp := c.counts("A"); e != 1 || cp != 1 {
		t.Fatalf("A counts exec/comp = %d/%d", e, cp)
	}
	if e, cp := c.counts("B"); e != 1 || cp != 1 {
		t.Fatalf("B counts exec/comp = %d/%d", e, cp)
	}
	if e, cp := c.counts("C"); e != 1 || cp != 0 {
		t.Fatalf("C (failed step) counts exec/comp = %d/%d, want 1/0", e, cp)
	}
}

func TestCompensationFailuresAggregated(t *testing.T) {
	// A -> B -> C ; C fails; compensations of B and A both fail.
	g := chainABC()
	c := newCounters()
	c.failExec["C"] = 1
	c.failComp["A"] = true
	c.failComp["B"] = true
	o, _ := New(g, Config{}, actionsFor(g, c))
	snap, err := o.Run()
	if err != nil {
		t.Fatal(err)
	}
	if snap.Phase != PhaseAborted {
		t.Fatalf("phase %s", snap.Phase)
	}
	if len(snap.CompFailures) != 2 {
		t.Fatalf("want 2 aggregated comp failures, got %+v", snap.CompFailures)
	}
	got := map[string]bool{}
	for _, cf := range snap.CompFailures {
		got[cf.StepID] = true
		if cf.Detail == "" {
			t.Fatal("missing detail")
		}
	}
	if !got["A"] || !got["B"] {
		t.Fatalf("aggregated set %v", got)
	}
}

func TestRetryBoundaryNPlusOneExecutions(t *testing.T) {
	// Single step, MaxRetries=2, retryable errors forever -> 3 executions.
	g := wideDAG(1)
	c := newCounters()
	c.failExec["n0"] = 100 // always fails
	pol := testRetryAllPolicy(2)
	o, _ := New(g, Config{Policy: pol}, actionsFor(g, c))
	snap, err := o.Run()
	if err != nil {
		t.Fatal(err)
	}
	if snap.Phase != PhaseAborted {
		t.Fatalf("phase %s", snap.Phase)
	}
	e, _ := c.counts("n0")
	if e != 3 {
		t.Fatalf("executions %d, want N+1=3", e)
	}
}

func TestNonRetryableFailsImmediately(t *testing.T) {
	g := wideDAG(1)
	c := newCounters()
	c.failExec["n0"] = 100
	o, _ := New(g, Config{}, actionsFor(g, c)) // default policy: no retry
	snap, _ := o.Run()
	e, _ := c.counts("n0")
	if e != 1 || snap.Phase != PhaseAborted {
		t.Fatalf("e=%d phase=%s", e, snap.Phase)
	}
}
