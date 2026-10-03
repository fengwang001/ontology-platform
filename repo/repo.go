// Package repo stores versioned definitions and creates pinned instances.
package repo

import (
	"errors"
	"fmt"
	"sync"

	"ontology/engine"
	"ontology/model"
)

// Rejection categories, checked in the order listed below.
var (
	ErrParam      = errors.New("repo: invalid parameter")
	ErrNoDef      = errors.New("repo: definition not found")
	ErrInstExists = errors.New("repo: instance already exists")
	ErrNoInst     = errors.New("repo: instance not found")
	ErrChoice     = errors.New("repo: invalid choices")
)

// Repo keeps every version of each definition plus the live instances.
type Repo struct {
	mu    sync.Mutex
	defs  map[string][]*model.Graph
	insts map[string]*engine.Instance
}

// New returns an empty repository.
func New() *Repo {
	return &Repo{defs: map[string][]*model.Graph{}, insts: map[string]*engine.Instance{}}
}

// Define validates g and appends it as the next version of defID.
// Rejected definitions do not consume a version number.
func (r *Repo) Define(defID string, g *model.Graph) (int, error) {
	if defID == "" || g == nil {
		return 0, ErrParam
	}
	if err := model.Validate(g); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.defs[defID] = append(r.defs[defID], g.Clone())
	return len(r.defs[defID]), nil
}

// Start pins the current latest version of defID under instance id inst.
func (r *Repo) Start(inst, defID string, choices map[int][]int) error {
	if inst == "" || defID == "" {
		return ErrParam
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	versions, ok := r.defs[defID]
	if !ok {
		return ErrNoDef
	}
	if _, dup := r.insts[inst]; dup {
		return ErrInstExists
	}
	g := versions[len(versions)-1]
	if err := checkChoices(g, choices); err != nil {
		return err
	}
	r.insts[inst] = engine.NewInstance(g, choices)
	return nil
}

// Complete finishes one activation of task on instance inst.
func (r *Repo) Complete(inst string, task int) error {
	in, err := r.instance(inst)
	if err != nil {
		return err
	}
	return in.Complete(task)
}

// Status returns a read-only snapshot of instance inst.
func (r *Repo) Status(inst string) (engine.Snapshot, error) {
	in, err := r.instance(inst)
	if err != nil {
		return engine.Snapshot{}, err
	}
	return in.Status(), nil
}

func (r *Repo) instance(inst string) (*engine.Instance, error) {
	if inst == "" {
		return nil, ErrParam
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	in, ok := r.insts[inst]
	if !ok {
		return nil, ErrNoInst
	}
	return in, nil
}

// checkChoices requires exactly one entry per XorSplit/OrSplit node:
// a single in-range index for XorSplit, a non-empty in-range subset
// without duplicates for OrSplit, and no entries for other nodes.
func checkChoices(g *model.Graph, choices map[int][]int) error {
	need := map[int]bool{}
	for v := 1; v <= g.N; v++ {
		if model.IsChoiceSplit(g.Kind[v]) {
			need[v] = true
		}
	}
	for v, sel := range choices {
		if !need[v] {
			return fmt.Errorf("%w: node %d is not a choice split", ErrChoice, v)
		}
		outDeg := len(g.Out(v))
		seen := map[int]bool{}
		for _, idx := range sel {
			if idx < 0 || idx >= outDeg {
				return fmt.Errorf("%w: edge index %d out of range at node %d", ErrChoice, idx, v)
			}
			if seen[idx] {
				return fmt.Errorf("%w: duplicate edge index %d at node %d", ErrChoice, idx, v)
			}
			seen[idx] = true
		}
		if g.Kind[v] == model.XorSplit && len(sel) != 1 {
			return fmt.Errorf("%w: XorSplit %d needs exactly 1 choice", ErrChoice, v)
		}
		if g.Kind[v] == model.OrSplit && len(sel) == 0 {
			return fmt.Errorf("%w: OrSplit %d needs a non-empty choice", ErrChoice, v)
		}
	}
	for v := range need {
		if _, ok := choices[v]; !ok {
			return fmt.Errorf("%w: missing choice for split %d", ErrChoice, v)
		}
	}
	return nil
}
