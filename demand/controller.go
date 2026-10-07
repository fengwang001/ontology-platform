package demand

import (
	"fmt"
	"math"
	"sync"
)

// ActionKind distinguishes cut and restore actions.
type ActionKind int

const (
	// ActionCut disconnects a load.
	ActionCut ActionKind = iota
	// ActionRestore reconnects a load.
	ActionRestore
)

func (k ActionKind) String() string {
	if k == ActionCut {
		return "cut"
	}
	return "restore"
}

// Action is one load switching decision produced by an evaluation.
type Action struct {
	Kind   ActionKind
	LoadID int
}

// ReportResult is the outcome of the evaluation triggered by one
// accepted report.
type ReportResult struct {
	// Actions lists the switching decisions of this evaluation: cuts
	// (sorted by load ID) or restores (in restore order). A single
	// evaluation never mixes cuts and restores.
	Actions []Action
	// StillViolating is true when, even after applying the actions,
	// some open window is still predicted to exceed the contract demand.
	StillViolating bool
}

// Controller is the maximum-demand controller. All methods are safe for
// concurrent use; a single mutex serializes them, so any concurrent call
// mix is equivalent to some sequential order (linearizable).
type Controller struct {
	mu   sync.Mutex
	cfg  Config
	hist *energyHistory
	reg  *loadRegistry
	peak peakTracker

	hasReport bool
	lastT     int64
	lastPower float64 // average power of the most recent report interval
	cumNow    float64 // cumulative energy at lastT
}

// NewController validates the configuration and creates a controller.
func NewController(cfg Config) (*Controller, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Controller{
		cfg:  cfg,
		hist: newEnergyHistory(cfg.WindowSeconds, cfg.SlipSeconds),
		reg:  newLoadRegistry(),
	}, nil
}

// nowLocked returns the controller's current time: the last accepted
// report timestamp, or 0 before the first report.
func (c *Controller) nowLocked() int64 {
	if c.hasReport {
		return c.lastT
	}
	return 0
}

// Report accepts one meter report: the timestamp and the energy (kW*s)
// consumed since the last accepted report. On success it runs one
// evaluation and returns its actions. Rejected reports change no state
// and do not advance time.
//
// Validation order: invalid parameter > illegal data > time regression.
// A regressed timestamp carrying positive energy implies infinite
// average power, hence illegal data (which outranks time regression).
//
// The very first report only sets the clock: its energy cannot be
// attributed to any interval and is ignored (documented trade-off).
func (c *Controller) Report(t int64, energyKWS float64) (ReportResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if t < 0 {
		return ReportResult{}, fmt.Errorf("%w: timestamp must be non-negative, got %d", ErrInvalidParam, t)
	}
	if math.IsNaN(energyKWS) || energyKWS < 0 {
		return ReportResult{}, fmt.Errorf("%w: energy must be non-negative, got %v", ErrInvalidParam, energyKWS)
	}
	if c.hasReport && t <= c.lastT {
		if energyKWS > 0 {
			return ReportResult{}, fmt.Errorf("%w: positive energy %v over non-positive interval implies infinite power", ErrDataIllegal, energyKWS)
		}
		return ReportResult{}, fmt.Errorf("%w: timestamp %d not greater than last accepted %d", ErrTimeRegression, t, c.lastT)
	}
	if c.hasReport {
		dt := t - c.lastT
		if energyKWS/float64(dt) > c.cfg.MaxPhysicalPowerKW {
			return ReportResult{}, fmt.Errorf("%w: average power %v exceeds physical limit %v", ErrDataIllegal, energyKWS/float64(dt), c.cfg.MaxPhysicalPowerKW)
		}
	}

	if !c.hasReport {
		c.hasReport = true
		c.lastT = t
		c.lastPower = 0
		c.cumNow = 0
		return c.evaluateLocked(t), nil
	}

	dt := t - c.lastT
	power := energyKWS / float64(dt)
	prevT := c.lastT

	// Close windows whose end boundary falls inside this interval and
	// record their measured demand before pruning the ledger.
	ends := c.hist.advance(prevT, t, c.cumNow, power)
	window := float64(c.cfg.WindowSeconds)
	for _, b := range ends {
		measured := (c.hist.cumAt(b) - c.hist.cumAt(b-c.cfg.WindowSeconds)) / window
		c.peak.record(measured, b)
	}
	c.hist.prune(t - c.cfg.WindowSeconds)

	c.cumNow += energyKWS
	c.lastT = t
	c.lastPower = power
	return c.evaluateLocked(t), nil
}
