// Package scaler implements a step-wise autoscaling controller with
// warm-up in-flight capacity, in-cooldown top-up deltas and scale-in
// blocking.
package scaler

import (
	"errors"
	"sync"
)

// Kind is the action kind of one Evaluate decision.
type Kind int

const (
	// KindNone means no action.
	KindNone Kind = iota
	// KindOut means scale-out (append an in-flight batch).
	KindOut
	// KindIn means scale-in (reduce ready capacity immediately).
	KindIn
)

func (k Kind) String() string {
	switch k {
	case KindOut:
		return "scale-out"
	case KindIn:
		return "scale-in"
	default:
		return "none"
	}
}

// Tier is one step of a tier table: it matches when lo<=v<next lo.
type Tier struct {
	Lo  int64
	Pct int64
}

// Config holds the constructor parameters; see New for constraints.
type Config struct {
	Mn int64 // capacity lower bound, 1..1e6
	Mx int64 // capacity upper bound, Mn..1e6
	C0 int64 // initial ready capacity, Mn..Mx

	H  int64 // scale-out threshold, 0..1e9
	Lw int64 // scale-in threshold, 0..1e9 and Lw<H

	OutTiers []Tier // scale-out tiers, lo strictly increasing from 0, pct 1..1000
	InTiers  []Tier // scale-in tiers, same format

	Ms   int64 // minimum scale-out step, 1..1e6
	W    int64 // warm-up duration, 1..1e9
	Cout int64 // scale-out cooldown, 0..1e9
	Cin  int64 // scale-in cooldown, 0..1e9
}

// Batch is an in-flight batch: Count instances ready at ReadyAt.
type Batch struct {
	ReadyAt int64
	Count   int64
}

// Result is the outcome of one accepted Evaluate.
type Result struct {
	Kind     Kind  // action kind
	Amount   int64 // instances added or removed this time (0 when none)
	Cap      int64 // ready capacity after processing
	InFlight int64 // total in-flight count after processing
}

var (
	// ErrInvalidArgument: now<0 or now>1e15, or metric<0 or metric>1e9.
	ErrInvalidArgument = errors.New("scaler: invalid argument")
	// ErrClockRegression: now is below the max accepted now.
	ErrClockRegression = errors.New("scaler: clock regression")
	// ErrInvalidConfig: constructor parameters are rejected as a whole.
	ErrInvalidConfig = errors.New("scaler: invalid config")
)

const (
	maxBound  = int64(1_000_000)
	maxThresh = int64(1_000_000_000)
	maxNow    = int64(1_000_000_000_000_000)
	maxPct    = int64(1000)
)

// Scaler is the step-wise autoscaling controller. All methods are safe
// for concurrent use; results equal some serial order.
type Scaler struct {
	mu  sync.Mutex
	cfg Config

	cap     int64   // ready capacity
	batches []Batch // in-flight batches, in append order
	b0      int64   // effective capacity before the last non-cooldown scale-out
	lastOut int64   // time of the last non-cooldown scale-out
	hasOut  bool
	lastIn  int64 // time of the last scale-in
	hasIn   bool
	maxNow  int64 // max accepted now
	lastNow int64 // (now, metric) of the last accepted evaluation,
	lastMet int64 // used to make repeated identical calls no-ops
	hasLast bool
}

// New validates the config; any violated constraint rejects the whole
// config with ErrInvalidConfig.
func New(cfg Config) (*Scaler, error) {
	if err := validate(cfg); err != nil {
		return nil, err
	}
	out := make([]Tier, len(cfg.OutTiers))
	copy(out, cfg.OutTiers)
	in := make([]Tier, len(cfg.InTiers))
	copy(in, cfg.InTiers)
	cfg.OutTiers, cfg.InTiers = out, in
	return &Scaler{cfg: cfg, cap: cfg.C0}, nil
}

func validate(cfg Config) error {
	if cfg.Mn < 1 || cfg.Mn > maxBound {
		return ErrInvalidConfig
	}
	if cfg.Mx < cfg.Mn || cfg.Mx > maxBound {
		return ErrInvalidConfig
	}
	if cfg.C0 < cfg.Mn || cfg.C0 > cfg.Mx {
		return ErrInvalidConfig
	}
	if cfg.H < 0 || cfg.H > maxThresh || cfg.Lw < 0 || cfg.Lw > maxThresh || cfg.Lw >= cfg.H {
		return ErrInvalidConfig
	}
	if !validTiers(cfg.OutTiers) || !validTiers(cfg.InTiers) {
		return ErrInvalidConfig
	}
	if cfg.Ms < 1 || cfg.Ms > maxBound {
		return ErrInvalidConfig
	}
	if cfg.W < 1 || cfg.W > maxThresh {
		return ErrInvalidConfig
	}
	if cfg.Cout < 0 || cfg.Cout > maxThresh || cfg.Cin < 0 || cfg.Cin > maxThresh {
		return ErrInvalidConfig
	}
	return nil
}

func validTiers(ts []Tier) bool {
	if len(ts) == 0 || ts[0].Lo != 0 {
		return false
	}
	for i, t := range ts {
		if t.Pct < 1 || t.Pct > maxPct {
			return false
		}
		if i > 0 && t.Lo <= ts[i-1].Lo {
			return false
		}
	}
	return true
}

// Evaluate processes one evaluation in the fixed order:
//  1. merge in-flight batches whose ready time <= now (equal is ready);
//  2. metric>=H: pick pct from the scale-out tiers; outside cooldown use
//     base=eff, inside cooldown use base=B0 and top up only the delta;
//  3. metric<Lw: pick pct from the scale-in tiers; blocked by any
//     in-flight batch or by the scale-in cooldown;
//  4. metric in [Lw,H): no action.
//
// Invalid arguments and clock regression are reported in this order,
// first match only; rejected calls change no state.
func (s *Scaler) Evaluate(now, metric int64) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now < 0 || now > maxNow || metric < 0 || metric > maxThresh {
		return Result{}, ErrInvalidArgument
	}
	if now < s.maxNow {
		return Result{}, ErrClockRegression
	}

	s.mergeReady(now)

	// Repeating the same metric at the same now is a no-op from the
	// second evaluation on.
	if s.hasLast && s.lastNow == now && s.lastMet == metric {
		s.maxNow = now
		return s.snapshot(KindNone, 0), nil
	}
	s.hasLast, s.lastNow, s.lastMet = true, now, metric

	res := s.decide(now, metric)
	s.maxNow = now
	return res, nil
}

// mergeReady folds batches with ReadyAt<=now into cap.
func (s *Scaler) mergeReady(now int64) {
	keep := s.batches[:0]
	for _, b := range s.batches {
		if b.ReadyAt <= now {
			s.cap += b.Count
		} else {
			keep = append(keep, b)
		}
	}
	s.batches = keep
}

func (s *Scaler) eff() int64 {
	e := s.cap
	for _, b := range s.batches {
		e += b.Count
	}
	return e
}

func (s *Scaler) snapshot(kind Kind, amount int64) Result {
	var inFlight int64
	for _, b := range s.batches {
		inFlight += b.Count
	}
	return Result{Kind: kind, Amount: amount, Cap: s.cap, InFlight: inFlight}
}

func (s *Scaler) decide(now, metric int64) Result {
	cfg := &s.cfg
	eff := s.eff()

	if metric >= cfg.H {
		pct := pickPct(cfg.OutTiers, metric-cfg.H)
		cooling := s.hasOut && now < s.lastOut+cfg.Cout
		base := eff
		if cooling {
			base = s.b0
		}
		delta := max64(cfg.Ms, ceilDiv100(base*pct))
		target := min64(cfg.Mx, base+delta)
		if target > eff {
			s.batches = append(s.batches, Batch{ReadyAt: now + cfg.W, Count: target - eff})
			if !cooling {
				s.b0 = eff
				s.lastOut = now
				s.hasOut = true
			}
			return s.snapshot(KindOut, target-eff)
		}
		return s.snapshot(KindNone, 0)
	}

	if metric < cfg.Lw {
		pct := pickPct(cfg.InTiers, cfg.Lw-metric)
		if len(s.batches) > 0 {
			return s.snapshot(KindNone, 0)
		}
		if s.hasIn && now < s.lastIn+cfg.Cin {
			return s.snapshot(KindNone, 0)
		}
		delta := max64(1, s.cap*pct/100)
		target := max64(cfg.Mn, s.cap-delta)
		if target < s.cap {
			amount := s.cap - target
			s.cap = target
			s.lastIn, s.hasIn = now, true
			return s.snapshot(KindIn, amount)
		}
		return s.snapshot(KindNone, 0)
	}

	return s.snapshot(KindNone, 0)
}

// pickPct selects the tier with lo<=v<next lo; the last tier is unbounded.
func pickPct(tiers []Tier, v int64) int64 {
	pct := tiers[0].Pct
	for i := 1; i < len(tiers); i++ {
		if v < tiers[i].Lo {
			break
		}
		pct = tiers[i].Pct
	}
	return pct
}

// ceilDiv100 computes ceil(x/100) for non-negative x.
func ceilDiv100(x int64) int64 {
	return (x + 99) / 100
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// Snapshot returns a copy of the internal state for tests and observers.
func (s *Scaler) Snapshot() (cap_ int64, batches []Batch, b0 int64, lastOut, lastIn int64, hasOut, hasIn bool, maxNow int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	batches = append([]Batch(nil), s.batches...)
	return s.cap, batches, s.b0, s.lastOut, s.lastIn, s.hasOut, s.hasIn, s.maxNow
}
