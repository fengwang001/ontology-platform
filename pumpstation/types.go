package pumpstation

import "errors"

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrTimeRollback    = errors.New("time rollback")
	ErrPumpNotFound    = errors.New("pump not found")
	ErrInvalidState    = errors.New("invalid pump state")
)

type ActionKind string

const (
	ActionStart ActionKind = "start"
	ActionStop  ActionKind = "stop"
)

type ActionReason string

const (
	ReasonNormalStart     ActionReason = "normal_start"
	ReasonNormalStop      ActionReason = "normal_stop"
	ReasonDryRun          ActionReason = "dry_run"
	ReasonOverflow        ActionReason = "overflow"
	ReasonFault           ActionReason = "fault"
	ReasonMaintenance     ActionReason = "maintenance"
	ReasonMaintenanceStop ActionReason = "maintenance_stop"
)

type PumpState string

const (
	PumpAvailable   PumpState = "available"
	PumpFaulted     PumpState = "faulted"
	PumpMaintenance PumpState = "maintenance"
)

type Config struct {
	PumpCount            int
	StartLevels          []int
	StopLevels           []int
	DryRunLevel          int
	DryRunRecoveryLevel  int
	OverflowLevel        int
	MinimumRunDuration   int64
	MinimumStopDuration  int64
	MinimumStartInterval int64
}

type Action struct {
	PumpID int
	Kind   ActionKind
	Reason ActionReason
}

type Evaluation struct {
	Time    int64
	Level   int
	Target  int
	DryLock bool
	Running []int
	Actions []Action
	Reason  string
}

type StateChange struct {
	Time    int64
	PumpID  int
	State   PumpState
	Actions []Action
	Reason  string
}

type PumpSnapshot struct {
	ID             int
	State          PumpState
	Running        bool
	CumulativeTime int64
	StartedAt      int64
	StoppedAt      int64
	LastStartedAt  int64
}

type Snapshot struct {
	Time           int64
	Level          int
	LevelKnown     bool
	Target         int
	DryLock        bool
	AvailablePumps int
	RunningPumps   int
	Pumps          []PumpSnapshot
}
