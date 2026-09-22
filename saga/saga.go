package saga

import (
	"ontology/journal"
	"ontology/step"
)

type Status int

const (
	Running Status = iota
	Completed
	Compensated
	CompensationFailed
)

type Config struct {
	MaxSteps          int
	MaxStepRetries    int
	MaxJournalEntries int
}

type State struct {
	Instance     string
	Status       Status
	ForwardOK    []bool
	Unknown      []bool
	CompOK       []bool
	FailedComp   []int
	Calls        map[int]int
	Steps        int
	FinishedAt   int64
}

type CheckReport struct {
	Instance    string
	LogsRead    int
	Reconciled  bool
}

type Orchestrator struct {
	now      func() int64
	journal  *journal.Journal
	cfg      Config
	logsRead int
}

func New(j *journal.Journal, now func() int64, cfg Config) *Orchestrator {
	return &Orchestrator{now: now, journal: j, cfg: cfg}
}

func (o *Orchestrator) Run(instance string, steps []step.Step) (*State, error) { return nil, nil }

func (o *Orchestrator) Resume(instance string, steps []step.Step) (*State, error) { return nil, nil }

func (o *Orchestrator) Status(instance string) (*State, error) { return nil, nil }

func (o *Orchestrator) Reconstruct(instance string, steps []step.Step) (*State, error) { return nil, nil }

func (o *Orchestrator) SelfCheck(instance string, steps []step.Step) (*CheckReport, error) { return nil, nil }
