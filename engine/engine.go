// Package engine executes token flow on a pinned definition version.
package engine

import (
	"errors"
	"sync"

	"ontology/model"
)

// ErrNoActive is returned by Complete when the task has no active token.
var ErrNoActive = errors.New("engine: task has no active token")

// State is the lifecycle state of an instance.
type State int

const (
	Running   State = iota // some task has act > 0
	Completed              // no active task and no stranded arrivals
	Stuck                  // no active task but tokens stranded at joins
)

func (s State) String() string {
	switch s {
	case Running:
		return "Running"
	case Completed:
		return "Completed"
	default:
		return "Stuck"
	}
}

// Snapshot is a read-only view of an instance.
type Snapshot struct {
	State    State
	EndCount int
	Fires    map[int]int // join node id -> trigger count
}

// Instance is a running flow: task activations plus per-in-edge arrivals.
type Instance struct {
	mu    sync.Mutex
	n     int
	kind  []model.NodeType
	out   [][]int // effective out-neighbors (choices applied), edge order
	pred  [][]int // structural in-neighbors of joins, 1-based
	joins []int   // join node ids, ascending
	reach []uint64
	act   map[int]int
	arr   map[[2]int]int
	end   int
	fires map[int]int
}

// NewInstance pins g with choices, emits the Start token and settles joins.
// g must pass model.Validate and choices must be valid for g.
func NewInstance(g *model.Graph, choices map[int][]int) *Instance {
	in := &Instance{
		n:     g.N,
		kind:  g.Kind,
		out:   make([][]int, g.N+1),
		pred:  make([][]int, g.N+1),
		act:   map[int]int{},
		arr:   map[[2]int]int{},
		fires: map[int]int{},
	}
	start := 0
	for v := 1; v <= g.N; v++ {
		outs := g.Out(v)
		if model.IsChoiceSplit(g.Kind[v]) {
			for _, idx := range choices[v] {
				in.out[v] = append(in.out[v], outs[idx].To)
			}
		} else {
			for _, e := range outs {
				in.out[v] = append(in.out[v], e.To)
			}
		}
		switch g.Kind[v] {
		case model.Start:
			start = v
		case model.AndJoin, model.OrJoin:
			in.joins = append(in.joins, v)
			in.fires[v] = 0
			for _, e := range g.In(v) {
				in.pred[v] = append(in.pred[v], e.From)
			}
		}
	}
	in.reach = closure(in.out, g.N)
	in.arrive(start, in.out[start][0])
	in.settle()
	return in
}

// Complete finishes one activation of task and lets the token flow on.
func (in *Instance) Complete(task int) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	if task < 1 || task > in.n || in.kind[task] != model.Task || in.act[task] == 0 {
		return ErrNoActive
	}
	if in.act[task]--; in.act[task] == 0 {
		delete(in.act, task)
	}
	in.arrive(task, in.out[task][0])
	in.settle()
	return nil
}

// Status returns a read-only snapshot of the instance.
func (in *Instance) Status() Snapshot {
	in.mu.Lock()
	defer in.mu.Unlock()
	st := Completed
	if len(in.act) > 0 {
		st = Running
	} else if len(in.arr) > 0 {
		st = Stuck
	}
	fires := make(map[int]int, len(in.fires))
	for j, c := range in.fires {
		fires[j] = c
	}
	return Snapshot{State: st, EndCount: in.end, Fires: fires}
}
