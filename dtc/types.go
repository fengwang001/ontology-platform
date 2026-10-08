package dtc

// Config holds the calibration values of the lifecycle manager.
// All debounce thresholds relate to a debounce counter that starts at 0:
//
//	DebouncePassLimit <= 0 <= DebounceFailLimit
type Config struct {
	// DebounceRiseStep is added to the debounce counter on each failed
	// monitor report (must be > 0).
	DebounceRiseStep int
	// DebounceFailLimit is the debounce counter value at which a
	// detection is judged as failed (must be > 0).
	DebounceFailLimit int
	// DebounceFallStep is subtracted from the debounce counter on each
	// passed monitor report (must be > 0).
	DebounceFallStep int
	// DebouncePassLimit is the debounce counter value at which a
	// detection is judged as passed (must be <= 0).
	DebouncePassLimit int
	// ConfirmCycles is the number of consecutive failed ignition
	// cycles required to confirm a DTC (must be >= 1).
	ConfirmCycles int
	// HealWarmupCycles is the number of consecutive fault-free warm-up
	// cycles (with completed monitoring) required to heal a confirmed
	// DTC (must be >= 1).
	HealWarmupCycles int
	// AutoClearWarmups is the total number of fault-free warm-up
	// cycles at which a healed DTC is fully erased (must be
	// >= HealWarmupCycles).
	AutoClearWarmups int
	// WarmupRise is the coolant temperature rise above the cycle
	// minimum required for a warm-up cycle (must be >= 0).
	WarmupRise int
	// WarmupFinalTemp is the coolant temperature that must have been
	// reached at least once for a warm-up cycle.
	WarmupFinalTemp int
}

func (c Config) validate() error {
	switch {
	case c.DebounceRiseStep <= 0:
		return errf(ErrInvalidParam, "debounce rise step must be > 0, got %d", c.DebounceRiseStep)
	case c.DebounceFailLimit <= 0:
		return errf(ErrInvalidParam, "debounce fail limit must be > 0, got %d", c.DebounceFailLimit)
	case c.DebounceFallStep <= 0:
		return errf(ErrInvalidParam, "debounce fall step must be > 0, got %d", c.DebounceFallStep)
	case c.DebouncePassLimit > 0:
		return errf(ErrInvalidParam, "debounce pass limit must be <= 0, got %d", c.DebouncePassLimit)
	case c.ConfirmCycles <= 0:
		return errf(ErrInvalidParam, "confirm cycles must be >= 1, got %d", c.ConfirmCycles)
	case c.HealWarmupCycles <= 0:
		return errf(ErrInvalidParam, "heal warm-up cycles must be >= 1, got %d", c.HealWarmupCycles)
	case c.AutoClearWarmups < c.HealWarmupCycles:
		return errf(ErrInvalidParam, "auto-clear warm-ups (%d) must be >= heal warm-up cycles (%d)",
			c.AutoClearWarmups, c.HealWarmupCycles)
	case c.WarmupRise < 0:
		return errf(ErrInvalidParam, "warm-up rise must be >= 0, got %d", c.WarmupRise)
	}
	return nil
}

// EventKind identifies the type of an Event.
type EventKind int

const (
	// EvIgnitionOn opens a new ignition cycle.
	EvIgnitionOn EventKind = iota + 1
	// EvIgnitionOff closes the current ignition cycle and settles it.
	EvIgnitionOff
	// EvMonitorResult reports a monitor result (Passed) for a DTC.
	EvMonitorResult
	// EvEnvSample carries an environment sample (Speed, Coolant).
	EvEnvSample
	// EvClear is a tester clear request (only allowed with ignition off).
	EvClear
)

func (k EventKind) String() string {
	switch k {
	case EvIgnitionOn:
		return "IgnitionOn"
	case EvIgnitionOff:
		return "IgnitionOff"
	case EvMonitorResult:
		return "MonitorResult"
	case EvEnvSample:
		return "EnvSample"
	case EvClear:
		return "Clear"
	}
	return "Unknown"
}

// Event is a single input to Manager.Handle. Time and Odometer must not
// be smaller than the ones of the last accepted event. Only the fields
// relevant for the Kind are inspected.
type Event struct {
	Kind     EventKind
	Time     int64 // event timestamp (monotonic, non-decreasing)
	Odometer int64 // odometer reading (non-decreasing)
	DTC      string
	Passed   bool // EvMonitorResult: true = pass, false = fail
	Speed    int  // EvEnvSample: vehicle speed
	Coolant  int  // EvEnvSample: coolant temperature
}

// Judgment is the debounced result of the current ignition cycle.
type Judgment int

const (
	// JudgmentNone means no debounced judgment exists (yet) this cycle.
	JudgmentNone Judgment = iota
	// JudgmentPass means the debounce counter reached the pass limit.
	JudgmentPass
	// JudgmentFail means the debounce counter reached the fail limit.
	JudgmentFail
)

func (j Judgment) String() string {
	switch j {
	case JudgmentPass:
		return "pass"
	case JudgmentFail:
		return "fail"
	}
	return "none"
}

// Snapshot is the query result for a single DTC.
type Snapshot struct {
	// Pending is set from the first failed judgment of a cycle until
	// the DTC heals or is cleared.
	Pending bool
	// Confirmed is set once enough consecutive failed cycles have been
	// observed, and cleared on heal or tester clear.
	Confirmed bool
	// Healed marks a formerly confirmed DTC whose confirmation and
	// pending flags were cleared after enough fault-free warm-up
	// cycles. The DTC is kept as history until auto-clear erases it.
	Healed bool
	// Judgment is the debounced judgment of the current ignition
	// cycle (JudgmentNone while ignition is off).
	Judgment Judgment
	// Occurrences counts how often the DTC was judged failed (at most
	// once per ignition cycle).
	Occurrences int
	// ConsecFailCycles is the current run of consecutive failed
	// ignition cycles.
	ConsecFailCycles int
	// FaultFreeWarmups is the current run of fault-free warm-up
	// cycles with completed monitoring.
	FaultFreeWarmups int
	// HasFreezeFrame reports whether this DTC currently owns the
	// single freeze frame slot.
	HasFreezeFrame bool
	// DistanceSinceClear is the distance driven since the last tester
	// clear (current odometer minus the clear baseline).
	DistanceSinceClear int64
}

// FreezeFrame is the data captured in the single freeze frame slot.
type FreezeFrame struct {
	// Occupied reports whether the slot is in use.
	Occupied bool
	// Owner is the DTC that captured the slot.
	Owner string
	// Speed and Coolant come from the most recent environment sample
	// at capture time.
	Speed   int
	Coolant int
	// Odometer and Time describe the moment of the failed judgment
	// that captured the slot.
	Odometer int64
	Time     int64
}
