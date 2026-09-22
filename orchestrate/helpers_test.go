package orchestrate

import (
	"errors"
	"sync"

	"ontology/graph"
	"ontology/step"
)

var errBoom = errors.New("boom")

// counters is an in-test action factory with readable exec/comp counts and
// controllable failures/delays.
type counters struct {
	mu       sync.Mutex
	execs    map[string]int
	comps    map[string]int
	failExec map[string]int // first N attempts fail, then succeed
	failComp map[string]bool
	release  map[string]chan struct{} // Execute blocks until this is closed
	started  map[string]chan struct{} // closed when Execute begins
	gate     chan struct{}            // if non-nil, Execute waits here globally
}

func newCounters() *counters {
	return &counters{
		execs:    map[string]int{},
		comps:    map[string]int{},
		failExec: map[string]int{},
		failComp: map[string]bool{},
		release:  map[string]chan struct{}{},
		started:  map[string]chan struct{}{},
	}
}

// enableGate makes every Execute block until releaseGate is called.
func (c *counters) enableGate() {
	c.mu.Lock()
	c.gate = make(chan struct{})
	c.mu.Unlock()
}

func (c *counters) releaseGate() {
	c.mu.Lock()
	g := c.gate
	c.mu.Unlock()
	if g != nil {
		close(g)
	}
}

func (c *counters) counts(id string) (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.execs[id], c.comps[id]
}

// hold makes Execute(id) block until release(id) is invoked.
func (c *counters) hold(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.release[id]; !ok {
		c.release[id] = make(chan struct{})
		c.started[id] = make(chan struct{})
	}
}

func (c *counters) waitStarted(id string) {
	c.mu.Lock()
	ch := c.started[id]
	c.mu.Unlock()
	if ch != nil {
		<-ch
	}
}

func (c *counters) releaseStep(id string) {
	c.mu.Lock()
	ch := c.release[id]
	c.mu.Unlock()
	if ch != nil {
		close(ch)
	}
}

func (c *counters) action(id string) step.Action {
	return step.Action{
		Execute: func() error {
			c.mu.Lock()
			c.execs[id]++
			n := c.execs[id]
			failUntil := c.failExec[id]
			rel := c.release[id]
			st := c.started[id]
			c.mu.Unlock()
			if st != nil {
				select {
				case <-st:
				default:
					close(st)
				}
			}
			if g := c.gate; g != nil {
				<-g
			}
			if rel != nil {
				<-rel
			}
			if n <= failUntil {
				return errBoom
			}
			return nil
		},
		Compensate: func() error {
			c.mu.Lock()
			c.comps[id]++
			fail := c.failComp[id]
			c.mu.Unlock()
			if fail {
				return errBoom
			}
			return nil
		},
	}
}

func actionsFor(g *graph.Graph, c *counters) map[string]step.Action {
	m := map[string]step.Action{}
	for _, id := range g.Nodes() {
		m[id] = c.action(id)
	}
	return m
}

func chainABC() *graph.Graph {
	b := graph.NewBuilder()
	_ = b.AddNode("A")
	_ = b.AddNode("B")
	_ = b.AddNode("C")
	_ = b.AddEdge("A", "B")
	_ = b.AddEdge("B", "C")
	g, _ := b.Build()
	return g
}

func wideDAG(width int) *graph.Graph {
	b := graph.NewBuilder()
	for i := 0; i < width; i++ {
		_ = b.AddNode("n" + itoa(i))
	}
	g, _ := b.Build()
	return g
}
