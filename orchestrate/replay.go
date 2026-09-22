package orchestrate

import (
	"ontology/graph"
	"ontology/journal"
	"ontology/step"
)

// Recover rebuilds an orchestrator purely from a journal storage image.
// No in-memory residue from the previous process is used.
//
// Cost: folding records is O(L) with ZERO graph node lookups. After folding,
// deriving the continuation plan touches each graph node exactly once,
// so graph node visits are O(N) with no dependence on L.
func Recover(g *graph.Graph, cfg Config, actions map[string]step.Action, image []byte, hook journal.CrashHook) (*Orchestrator, Snapshot, error) {
	if err := cfg.validate(); err != nil {
		return nil, Snapshot{}, err
	}
	if cfg.MaxSteps > 0 && g.Len() > cfg.MaxSteps {
		return nil, Snapshot{}, ErrStepLimit
	}
	if cfg.Clock == nil {
		cfg.Clock = noopClock{}
	}
	j, err := journal.Load(image, cfg.MaxJournalRecords, hook)
	if err != nil {
		return nil, Snapshot{}, resolveError(err)
	}
	o := &Orchestrator{
		g:        g,
		actions:  make(map[string]step.Action, len(actions)),
		cfg:      cfg,
		j:        j,
		ex:       step.NewExecutor(j),
		machines: make(map[string]*step.Machine, g.Len()),
	}
	for id, a := range actions {
		if g.Has(id) {
			o.actions[id] = a
		}
	}
	for _, id := range g.Nodes() {
		o.machines[id] = step.NewMachine(id)
	}
	// O(L) fold, zero graph accesses.
	for _, r := range j.Snapshot() {
		m := o.machines[r.StepID]
		if m == nil {
			m = step.NewMachine(r.StepID)
			o.machines[r.StepID] = m
		}
		if err := m.Apply(r); err != nil {
			return nil, Snapshot{}, err
		}
	}
	o.derivePlan() // exactly O(N) graph node visits
	return o, o.snapshotLocked(), nil
}

// derivePlan inspects every graph node once to decide the visible phase
// after replay. Each node lookup counts as one graph node visit.
func (o *Orchestrator) derivePlan() {
	anyFailed := false
	anyRunning := false
	allTerminal := true
	for _, id := range o.g.Nodes() {
		o.graphVisits++
		m := o.machines[id]
		if m == nil {
			allTerminal = false
			continue
		}
		switch m.State() {
		case step.Failed, step.CompFailed, step.Compensated:
			anyFailed = true
		case step.Running, step.Compensating:
			anyRunning = true
			allTerminal = false
		case step.Pending:
			allTerminal = false
		}
	}
	switch {
	case anyFailed:
		o.phase = PhaseAborted
		o.terminal = allTerminal && !anyRunning
	case allDoneLocked(o):
		o.phase = PhaseCompleted
		o.terminal = true
	case anyRunning:
		o.phase = PhaseExecuting
	default:
		o.phase = PhaseIdle
	}
	// Re-aggregate compensation failures purely from machines.
	o.compFails = nil
	var failMsg string
	for _, id := range o.g.Nodes() {
		o.graphVisits++
		m := o.machines[id]
		if m != nil && m.State() == step.CompFailed {
			o.compFails = append(o.compFails, CompFailure{StepID: id, Detail: m.CompFailMessage()})
		}
		if m != nil && m.State() == step.Failed && failMsg == "" {
			failMsg = m.FailMessage()
		}
	}
	if failMsg != "" {
		o.failure = failMsg
	}
}

func allDoneLocked(o *Orchestrator) bool {
	for _, id := range o.g.Nodes() {
		o.graphVisits++
		m := o.machines[id]
		if m == nil || m.State() != step.Done {
			return false
		}
	}
	return true
}

// graphVisitCount exposes the unexported counter to in-package tests.
func (o *Orchestrator) graphVisitCount() int {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.graphVisits
}
