// Package saga implements an in-memory, compensating SAGA orchestrator. Time
// is injected; the package never reads the wall clock or sleeps.
package saga

import (
	"context"
	"errors"
	"sync"

	"ontology/journal"
	"ontology/step"
)

// Status is the reconstructed lifecycle status of one instance.
type Status int

const (
	StatusRunning      Status = iota // forward phase not finished
	StatusSucceeded                  // all steps forward-succeeded
	StatusCompensating               // compensation started, not all done
	StatusCompensated                // compensation finished with no failure
	StatusPartialComp                // compensation finished but some steps failed
)

func (s Status) String() string {
	switch s {
	case StatusSucceeded:
		return "succeeded"
	case StatusCompensating:
		return "compensating"
	case StatusCompensated:
		return "compensated"
	case StatusPartialComp:
		return "partially-compensated"
	default:
		return "running"
	}
}

// StepState is the reconstructed per-step state.
type StepState struct {
	Index      int
	Name       string
	IdemKey    string
	Forward    journal.Kind // 0, ForwardOK, ForwardUncertain or ForwardFailed
	ForwardErr string
	Comp       journal.Kind // 0, Compensated or CompFailed
	CompErr    string
}

// State is the full state reconstructable from one instance's log alone.
type State struct {
	Instance      string
	Status        Status
	Steps         []StepState
	FailedComp    []int // indexes whose compensation ended in failure
	PhysicalCalls int   // physical action invocations recorded
	LastRecordSeq int64
	LastError     string
}

// Config bounds resource use. Zero Max* fields mean unlimited.
type Config struct {
	MaxSteps       int
	MaxStepRetries int
	MaxJournalRows int
	Now            func() int64
}

type instance struct {
	def     []step.Step
	running bool
}

// Orchestrator runs and resumes SAGA instances over one journal.
type Orchestrator struct {
	cfg Config
	j   *journal.Journal
	mu  sync.Mutex
	reg map[string]*instance

	// recordsRead is unexported test instrumentation: how many log records
	// the most recent Resume physically consumed.
	recordsRead int
}

// New builds an orchestrator. A nil clock is rejected at construction.
func New(cfg Config, j *journal.Journal) (*Orchestrator, error) {
	if cfg.Now == nil {
		return nil, errors.New("saga: injected clock Now is required")
	}
	if j == nil {
		return nil, errors.New("saga: journal is required")
	}
	return &Orchestrator{cfg: cfg, j: j, reg: map[string]*instance{}}, nil
}

func validate(steps []step.Step, cfg Config) error {
	if len(steps) == 0 {
		return ErrNoSteps
	}
	if cfg.MaxSteps > 0 && len(steps) > cfg.MaxSteps {
		return ErrMaxSteps
	}
	seen := map[string]bool{}
	for i, st := range steps {
		if st.IdemKey == "" {
			return errors.New("saga: empty idempotency key")
		}
		if seen[st.IdemKey] {
			return ErrDuplicateKey
		}
		seen[st.IdemKey] = true
		if st.Do == nil {
			return errors.New("saga: forward action is nil")
		}
		if st.Retries < 0 || (cfg.MaxStepRetries > 0 && st.Retries > cfg.MaxStepRetries) {
			return ErrMaxStepRetries
		}
		_ = i
	}
	return nil
}

// Run registers and starts a new instance.
func (o *Orchestrator) Run(ctx context.Context, id string, steps []step.Step) (State, error) {
	if err := validate(steps, o.cfg); err != nil {
		return State{}, err
	}
	o.mu.Lock()
	if _, exists := o.reg[id]; exists {
		o.mu.Unlock()
		return State{}, errors.New("saga: instance already exists: " + id)
	}
	inst := &instance{def: steps, running: true}
	o.reg[id] = inst
	o.mu.Unlock()
	defer func() {
		o.mu.Lock()
		inst.running = false
		o.mu.Unlock()
	}()
	if err := o.execute(ctx, id, steps); err != nil {
		return State{}, err
	}
	return o.State(id)
}

// Resume continues a previously started instance toward its terminal state.
func (o *Orchestrator) Resume(ctx context.Context, id string) (State, error) {
	o.mu.Lock()
	inst, ok := o.reg[id]
	if !ok {
		o.mu.Unlock()
		return State{}, ErrInstanceNotFound
	}
	if inst.running {
		o.mu.Unlock()
		return State{}, ErrInstanceRunning
	}
	inst.running = true
	steps := inst.def
	o.mu.Unlock()
	defer func() {
		o.mu.Lock()
		inst.running = false
		o.mu.Unlock()
	}()

	// Fast terminal check: read only the final record first.
	last, has := o.j.Last(id)
	o.mu.Lock()
	o.recordsRead = 0
	o.mu.Unlock()
	if has {
		o.countRead(1)
		if last.Kind == journal.MarkForwardDone || last.Kind == journal.MarkCompDone {
			return o.State(id)
		}
	}
	rs := o.j.Read(id)
	o.countRead(len(rs))
	if err := o.executeFrom(ctx, id, steps, rs); err != nil {
		return State{}, err
	}
	return o.State(id)
}

func (o *Orchestrator) countRead(n int) {
	o.mu.Lock()
	o.recordsRead += n
	o.mu.Unlock()
}
