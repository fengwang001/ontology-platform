package orchestrate

import (
	"errors"
	"sync"

	"ontology/graph"
	"ontology/journal"
	"ontology/policy"
	"ontology/step"
)

// Orchestrator runs one DAG workflow to one of two terminal outcomes.
// All mutable state is guarded by mu; the journal is the single source of
// truth and the only thing consulted on recovery.
type Orchestrator struct {
	mu            sync.RWMutex
	g             *graph.Graph
	actions       map[string]step.Action
	cfg           Config
	j             *journal.Journal
	ex            *step.Executor
	machines      map[string]*step.Machine
	phase         Phase
	maxConc       int
	activeRunners int
	failure       string
	compFails     []CompFailure
	terminal      bool

	// graphVisits counts node-level lookups performed while deriving a
	// resume/run plan from the graph. It is deliberately unexported.
	graphVisits int

	crashMu sync.Mutex
	crash   interface{}
}

// New constructs an orchestrator for an already-validated graph. A nil
// clock installs a no-wait clock; the journal starts empty with no hook.
func New(g *graph.Graph, cfg Config, actions map[string]step.Action) (*Orchestrator, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if cfg.MaxSteps > 0 && g.Len() > cfg.MaxSteps {
		return nil, ErrStepLimit
	}
	if cfg.Clock == nil {
		cfg.Clock = noopClock{}
	}
	acts := make(map[string]step.Action, len(actions))
	for id, a := range actions {
		if !g.Has(id) {
			// Ignore entries for unknown ids; missing ids are checked at run.
			continue
		}
		acts[id] = a
	}
	o := &Orchestrator{
		g:        g,
		actions:  acts,
		cfg:      cfg,
		machines: make(map[string]*step.Machine, g.Len()),
		phase:    PhaseIdle,
	}
	o.j = journal.New(cfg.MaxJournalRecords, o.crashHook)
	o.ex = step.NewExecutor(o.j)
	for _, id := range g.Nodes() {
		o.machines[id] = step.NewMachine(id)
	}
	return o, nil
}

// SetHook installs a physical-write crash hook. It is only valid before
// the first Run, on a fresh or freshly recovered orchestrator.
func (o *Orchestrator) SetHook(h journal.CrashHook) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.terminal || o.phase != PhaseIdle {
		return ErrAlreadyTerminal
	}
	img := o.j.Bytes()
	nj, err := journal.Load(img, o.cfg.MaxJournalRecords, h)
	if err != nil {
		return resolveError(err)
	}
	o.j = nj
	o.ex = step.NewExecutor(o.j)
	return nil
}

func (o *Orchestrator) crashHook(r journal.Record, p journal.CrashPoint) {}

// noopClock is a zero-cost clock when retries do not sleep.
type noopClock struct{}

func (noopClock) NowMillis() int64 { return 0 }
func (noopClock) Sleep(int64) bool { return true }

var _ policy.Clock = noopClock{}

// IsTerminal reports whether the run already reached an end state.
func (o *Orchestrator) IsTerminal() bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.terminal
}

// resolveError maps journal errors at every call site.
func resolveError(err error) error {
	if errors.Is(err, journal.ErrLimit) {
		return ErrJournalLimit
	}
	return err
}
