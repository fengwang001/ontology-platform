package orchestrate

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"ontology/graph"
	"ontology/journal"
	"ontology/policy"
	"ontology/step"
)

// Config assembles an Orchestrator. Journal may be nil (empty trail) or
// carry a recovered trail; recovery derives state from it alone.
type Config struct {
	Graph   *graph.Graph
	Steps   []step.Step
	Journal *journal.Journal
	Policy  policy.Policy
	Clock   policy.Clock // nil selects a fake clock started at zero time
	Limits  Limits
	Hook    journal.Hook // crash-injection hook, may be nil
}

// Orchestrator runs one workflow instance.
type Orchestrator struct {
	layers [][]string
	topo   []string
	rev    []string
	defs   map[string]step.Step
	mach   map[string]*step.Machine
	j      *journal.Journal
	pol    policy.Policy
	clock  policy.Clock
	limits Limits

	mu                sync.RWMutex // guards phase; compensation holds it exclusively
	phase             Phase
	needsCompensation bool
	sem               chan struct{}
	inflight          atomic.Int64
	maxInflight       atomic.Int64
	numSteps          int

	// replayNodeVisits counts graph-node touches during journal replay.
	// It is O(number of steps), independent of trail length.
	replayNodeVisits int
}

// New validates the graph and limits, replays the journal (if any), and
// returns a ready orchestrator. Limit violations are rejected before any
// state exists, so rejection changes nothing.
func New(cfg Config) (*Orchestrator, error) {
	if cfg.Graph == nil {
		return nil, fmt.Errorf("orchestrate: nil graph")
	}
	if err := cfg.Graph.Validate(); err != nil {
		return nil, err
	}
	j := cfg.Journal
	if j == nil {
		j = journal.New(cfg.Limits.MaxJournalRecords, cfg.Hook)
	}
	clock := cfg.Clock
	if clock == nil {
		clock = policy.NewFakeClock(time.Unix(0, 0))
	}
	conc := cfg.Limits.MaxConcurrency
	if conc == 0 {
		conc = DefaultConcurrency
	}
	if err := validateLimits(cfg.Limits, cfg.Graph.Len(), j.Len()); err != nil {
		return nil, err
	}
	o := &Orchestrator{
		layers:   cfg.Graph.Layers(),
		topo:     cfg.Graph.TopoOrder(),
		rev:      cfg.Graph.ReverseTopoOrder(),
		defs:     make(map[string]step.Step, len(cfg.Steps)),
		mach:     make(map[string]*step.Machine, cfg.Graph.Len()),
		j:        j,
		pol:      cfg.Policy,
		clock:    clock,
		limits:   cfg.Limits,
		sem:      make(chan struct{}, conc),
		numSteps: cfg.Graph.Len(),
	}
	for _, s := range cfg.Steps {
		o.defs[s.ID] = s
	}
	for _, id := range cfg.Graph.Nodes() {
		o.mach[id] = step.NewMachine()
	}
	o.replay()
	return o, nil
}

// Recover rebuilds an orchestrator from raw journal bytes alone. A torn
// tail is dropped by the journal; torn reports whether that happened.
func Recover(cfg Config, journalBytes []byte) (o *Orchestrator, torn bool, err error) {
	j, torn := journal.FromBytes(journalBytes, cfg.Limits.MaxJournalRecords, cfg.Hook)
	cfg.Journal = j
	o, err = New(cfg)
	return o, torn, err
}

// replay folds the trail into the step machines. Cost is O(L + N): one
// hash-map lookup per record, then a single pass over the graph nodes.
// No graph traversal happens per record, so replayNodeVisits stays O(N)
// no matter how long the trail grows.
func (o *Orchestrator) replay() {
	for _, rec := range o.j.Records() {
		if m, ok := o.mach[rec.StepID]; ok {
			m.Apply(rec)
		}
	}
	for _, m := range o.mach {
		o.replayNodeVisits++
		m.ResetAfterCrash()
		switch m.Status() {
		case step.Failed, step.Compensated, step.CompensateFailed:
			o.needsCompensation = true
		}
	}
}

func validateLimits(l Limits, numSteps, journalLen int) error {
	if l.MaxSteps > 0 && numSteps > l.MaxSteps {
		return fmt.Errorf("%w: %d > %d", ErrTooManySteps, numSteps, l.MaxSteps)
	}
	c := l.MaxConcurrency
	if c < 0 || c > HardMaxConcurrency {
		return fmt.Errorf("%w: %d", ErrConcurrencyLimit, c)
	}
	if l.MaxJournalRecords > 0 && journalLen >= l.MaxJournalRecords {
		return fmt.Errorf("%w: %d records already stored", ErrJournalTooLong, journalLen)
	}
	return nil
}

func (o *Orchestrator) checkLimits() error {
	return validateLimits(o.limits, o.numSteps, o.j.Len())
}

// Snapshot returns a consistent read-only view. It never mutates state;
// during compensation it blocks until the terminal state is visible.
func (o *Orchestrator) Snapshot() Snapshot {
	o.mu.RLock()
	defer o.mu.RUnlock()
	snap := Snapshot{
		Phase:      o.phase,
		Steps:      make(map[string]StepInfo, len(o.mach)),
		JournalLen: o.j.Len(),
	}
	for id, m := range o.mach {
		snap.Steps[id] = o.infoOf(m)
	}
	return snap
}

// StepInfo returns the view of one step; unknown IDs yield the zero value.
func (o *Orchestrator) StepInfo(id string) StepInfo {
	o.mu.RLock()
	defer o.mu.RUnlock()
	m, ok := o.mach[id]
	if !ok {
		return StepInfo{}
	}
	return o.infoOf(m)
}

func (o *Orchestrator) infoOf(m *step.Machine) StepInfo {
	exec, comp := m.Counts()
	return StepInfo{Status: m.Status(), ExecCount: exec, CompCount: comp}
}

// JournalBytes returns a copy of the raw trail.
func (o *Orchestrator) JournalBytes() []byte { return o.j.Bytes() }

// MaxObservedConcurrency reports the peak number of steps running at once.
func (o *Orchestrator) MaxObservedConcurrency() int {
	return int(o.maxInflight.Load())
}

// ReplayNodeVisits exposes the replay-time graph-node visit counter.
func (o *Orchestrator) ReplayNodeVisits() int { return o.replayNodeVisits }
