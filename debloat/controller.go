// Package debloat implements a network buffer de-bloat controller with a
// sliding throughput window, hysteresis confirmation and a bounded memory pool.
package debloat

import (
	"errors"
	"math/big"
	"sync"
)

// Direction records the direction of the most recent size change.
type Direction int

const (
	DirNone Direction = iota
	DirUp
	DirDown
)

func (d Direction) String() string {
	switch d {
	case DirUp:
		return "up"
	case DirDown:
		return "down"
	default:
		return "none"
	}
}

// Action is the outcome of a Sample call.
type Action int

const (
	ActionRejected Action = iota
	ActionSkipped
	ActionHold
	ActionPending
	ActionApplied
)

func (a Action) String() string {
	switch a {
	case ActionSkipped:
		return "Skipped"
	case ActionHold:
		return "Hold"
	case ActionPending:
		return "Pending"
	case ActionApplied:
		return "Applied"
	default:
		return "Rejected"
	}
}

// Config holds the construction parameters of a Controller.
type Config struct {
	Bmin uint64 // buffer lower bound (>= 1, multiple of G)
	Bmax uint64 // buffer upper bound (<= 2^30, multiple of G)
	G    uint64 // granularity (>= 1)
	B0   uint64 // initial size (Bmin <= B0 <= Bmax, multiple of G)
	T    uint64 // target latency in milliseconds (1..1_000_000)
	W    int    // sliding window sample count (1..100)
	ThU  uint64 // grow threshold percent (0..1000)
	ThD  uint64 // shrink threshold percent (0..100)
	Kc   int    // grow confirmation count (1..100)
	C0   int    // initial channel count (1..10_000)
	Pool uint64 // memory pool bytes (1..2^40)
	H    int    // oscillation suppression window (0..1000)
}

// Result is the value returned by a successful Sample call.
type Result struct {
	Action Action
	Cur    uint64
	R      uint64 // measured throughput: floor(sumBytes*1000/sumDt)
	Cand   uint64 // granularity-rounded clamped candidate
	Streak int
}

// Snapshot is the full observable state returned by State.
type Snapshot struct {
	Cur          uint64
	Channels     int
	WindowLen    int
	Streak       int
	Polluted     bool
	Paused       bool
	LastDir      Direction
	Gap          int
	Damp         int
	AppliedCount int
	ForcedCount  int
	SkippedCount int
	WindowOps    int
}

// ErrIllegalConfig is returned when construction parameters are invalid.
var ErrIllegalConfig = errors.New("debloat: illegal configuration")

// ErrIllegalArgument is returned when an operation argument is out of range.
var ErrIllegalArgument = errors.New("debloat: illegal argument")

// ErrPaused is returned when Sample is called while paused.
var ErrPaused = errors.New("debloat: controller is paused")

// ErrInsufficientCapacity is returned when SetChannels cannot satisfy Bmin.
var ErrInsufficientCapacity = errors.New("debloat: insufficient pool capacity")

// Controller is the de-bloat controller. All methods are safe for concurrent
// use; their effects are equivalent to some serial execution order.
type Controller struct {
	mu sync.Mutex

	cfg Config

	cur   uint64
	chans int

	// Sliding window implemented as a fixed-capacity ring. wb[i]/wd[i] are
	// the accepted sample's bytes/dt at ring slot (head+k)%W.
	head int
	wlen int
	wb   []uint64
	wd   []uint64
	sumB uint64
	sumD uint64

	streak   int
	polluted bool
	paused   bool
	lastDir  Direction
	gap      int
	damp     int

	appliedCount int
	forcedCount  int
	skippedCount int

	// windowOps counts window insertion and eviction operations. An accepted
	// non-Skipped Sample performs at most 2: one insertion, plus one eviction
	// when the window is already full. The increment is therefore identical
	// for any W once the window has filled.
	windowOps int
}

// effectiveCap returns Beff(C) = min(Bmax, floor(Pool/(C*G))*G).
func effectiveCap(cfg Config, channels int) uint64 {
	q := cfg.Pool / (uint64(channels) * cfg.G)
	capByPool := q * cfg.G
	if cfg.Bmax < capByPool {
		return cfg.Bmax
	}
	return capByPool
}

func validConfig(cfg Config) bool {
	const (
		maxSize = uint64(1) << 30
		maxPool = uint64(1) << 40
	)
	if cfg.G < 1 || cfg.G > maxSize {
		return false
	}
	if !(cfg.G <= cfg.Bmin) || cfg.Bmin > maxSize {
		return false
	}
	if !(cfg.Bmin <= cfg.B0) || cfg.B0 > maxSize {
		return false
	}
	if !(cfg.B0 <= cfg.Bmax) || cfg.Bmax > maxSize {
		return false
	}
	if cfg.Bmin%cfg.G != 0 || cfg.B0%cfg.G != 0 || cfg.Bmax%cfg.G != 0 {
		return false
	}
	if cfg.T < 1 || cfg.T > 1_000_000 {
		return false
	}
	if cfg.W < 1 || cfg.W > 100 {
		return false
	}
	if cfg.ThU > 1000 || cfg.ThD > 100 {
		return false
	}
	if cfg.Kc < 1 || cfg.Kc > 100 {
		return false
	}
	if cfg.C0 < 1 || cfg.C0 > 10_000 {
		return false
	}
	if cfg.Pool < 1 || cfg.Pool > maxPool {
		return false
	}
	if cfg.H < 0 || cfg.H > 1000 {
		return false
	}
	return true
}

// New constructs a Controller, rejecting the whole configuration if any
// constraint is violated.
func New(cfg Config) (*Controller, error) {
	if !validConfig(cfg) {
		return nil, ErrIllegalConfig
	}
	if effectiveCap(cfg, cfg.C0) < cfg.B0 {
		return nil, ErrIllegalConfig
	}
	return &Controller{
		cfg:   cfg,
		cur:   cfg.B0,
		chans: cfg.C0,
		wb:    make([]uint64, cfg.W),
		wd:    make([]uint64, cfg.W),
	}, nil
}

// Sample processes one throughput observation.
func (c *Controller) Sample(bytes uint64, dt uint64) (Result, error) {
	if bytes > (uint64(1)<<30) || dt < 1 || dt > 1_000_000 {
		return Result{}, ErrIllegalArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.paused {
		return Result{}, ErrPaused
	}

	// Polluted sample: clear the flag and skip everything else. The window,
	// streak, gap and damp are all left untouched.
	if c.polluted {
		c.polluted = false
		c.skippedCount++
		return Result{Action: ActionSkipped, Cur: c.cur, Streak: c.streak}, nil
	}

	if c.lastDir != DirNone {
		c.gap++
	}

	// Insert into the sliding window (1 op), evicting the oldest sample when
	// the window is already full (1 more op, at most 2 ops per Sample).
	slot := (c.head + c.wlen) % c.cfg.W
	if c.wlen == c.cfg.W {
		old := c.head
		c.sumB -= c.wb[old]
		c.sumD -= c.wd[old]
		c.head = (c.head + 1) % c.cfg.W
		slot = old
		c.windowOps += 2
	} else {
		c.windowOps++
	}
	c.wb[slot] = bytes
	c.wd[slot] = dt
	c.sumB += bytes
	c.sumD += dt
	if c.wlen < c.cfg.W {
		c.wlen++
	}

	// R = floor(Sb*1000/Sd). Sb <= 100*2^30 so R fits uint64, but R*T in the
	// next step can reach ~10^20 and is evaluated with arbitrary precision.
	rBig := new(big.Int).SetUint64(c.sumB)
	rBig.Mul(rBig, big.NewInt(1000))
	rBig.Quo(rBig, new(big.Int).SetUint64(c.sumD))

	// Dt = ceil(R*T/1000); per = ceil(Dt/C).
	dtBig := new(big.Int).Mul(rBig, new(big.Int).SetUint64(c.cfg.T))
	dtBig.Add(dtBig, big.NewInt(999))
	dtBig.Quo(dtBig, big.NewInt(1000))
	perBig := new(big.Int).Add(dtBig, big.NewInt(int64(c.chans)-1))
	perBig.Quo(perBig, big.NewInt(int64(c.chans)))

	r := rBig.Uint64()
	beff := effectiveCap(c.cfg, c.chans)

	raw := perBig.Uint64()
	if raw < c.cfg.Bmin {
		raw = c.cfg.Bmin
	}
	if raw > beff {
		raw = beff
	}
	cand := raw / c.cfg.G * c.cfg.G

	res := Result{Cur: c.cur, R: r, Cand: cand}
	need := c.cfg.Kc
	if c.damp > 0 {
		need = 2 * c.cfg.Kc
	}
	dampSetHere := false

	switch {
	case cand == c.cur:
		c.streak = 0
		res.Action = ActionHold
	case cand > c.cur:
		deltaPct := (cand - c.cur) * 100
		if deltaPct >= c.cur*c.cfg.ThU || cand == beff {
			c.streak++
			if c.streak >= need {
				c.cur = cand
				c.streak = 0
				c.polluted = true
				c.appliedCount++
				dampSetHere = c.recordChange(DirUp)
				res.Action = ActionApplied
			} else {
				res.Action = ActionPending
			}
		} else {
			c.streak = 0
			res.Action = ActionHold
		}
	default: // cand < c.cur
		deltaPct := (c.cur - cand) * 100
		if deltaPct >= c.cur*c.cfg.ThD || cand == c.cfg.Bmin {
			c.cur = cand
			c.streak = 0
			c.polluted = true
			c.appliedCount++
			dampSetHere = c.recordChange(DirDown)
			res.Action = ActionApplied
		} else {
			c.streak = 0
			res.Action = ActionHold
		}
	}

	res.Cur = c.cur
	res.Streak = c.streak

	// Every accepted non-Skipped Sample ages the suppression residual by one,
	// except the Sample that just (re)armed it; need was read from the value
	// at the start of processing this Sample.
	if c.damp > 0 && !dampSetHere {
		c.damp--
	}

	return res, nil
}

// recordChange applies the oscillation-suppression and direction/gap
// bookkeeping for one change (an Applied Sample or a forced shrink). It
// reports whether the change (re)armed damp.
func (c *Controller) recordChange(dir Direction) bool {
	set := false
	if c.lastDir != DirNone && dir != c.lastDir && c.gap <= c.cfg.H {
		c.damp = c.cfg.H
		set = true
	}
	c.lastDir = dir
	c.gap = 0
	return set
}

// SetChannels changes the channel count, force-shrinking if necessary.
func (c *Controller) SetChannels(channels int) error {
	if channels < 1 || channels > 10_000 {
		return ErrIllegalArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if channels == c.chans {
		return nil
	}
	beff := effectiveCap(c.cfg, channels)
	if beff < c.cfg.Bmin {
		return ErrInsufficientCapacity
	}

	c.chans = channels
	c.streak = 0
	if c.cur > beff {
		c.cur = beff
		c.polluted = true
		c.forcedCount++
		c.recordChange(DirDown)
	}
	return nil
}

// Pause sets the paused flag.
func (c *Controller) Pause() {
	c.mu.Lock()
	c.paused = true
	c.mu.Unlock()
}

// Resume clears the paused flag, window and streak, leaving pol/gap/damp/
// lastDir untouched.
func (c *Controller) Resume() {
	c.mu.Lock()
	c.paused = false
	c.head = 0
	c.wlen = 0
	c.sumB = 0
	c.sumD = 0
	c.streak = 0
	// pol, gap, damp and lastDir are intentionally preserved.
	c.mu.Unlock()
}

// State returns the full observable state and cumulative counters.
func (c *Controller) State() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Snapshot{
		Cur:          c.cur,
		Channels:     c.chans,
		WindowLen:    c.wlen,
		Streak:       c.streak,
		Polluted:     c.polluted,
		Paused:       c.paused,
		LastDir:      c.lastDir,
		Gap:          c.gap,
		Damp:         c.damp,
		AppliedCount: c.appliedCount,
		ForcedCount:  c.forcedCount,
		SkippedCount: c.skippedCount,
		WindowOps:    c.windowOps,
	}
}
