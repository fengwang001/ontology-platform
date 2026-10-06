// Package canary implements a gray-release traffic splitter with metric
// gates, automatic rollback, stable bucket ownership and session stickiness.
//
// All time values are caller-supplied Unix milliseconds; the splitter never
// reads a wall clock. A single mutex guards every exported entry point, so
// concurrent calls are equivalent to some serial ordering. Routing and
// observation touch only O(P) bounded sticky storage and O(1) counters, so
// their cost never grows with the number of historical requests.
package canary

import (
	"container/list"
	"errors"
	"hash/fnv"
	"math/big"
	"math/bits"
	"sync"
)

// Basis is the granularity of all ratios: one ten-thousandth.
const Basis = 10000

// Sentinel errors, checked by the caller via errors.Is.
var (
	// ErrInvalidArgument is reported when a parameter violates its contract.
	ErrInvalidArgument = errors.New("canary: invalid argument")
	// ErrClockWentBack is reported when now is earlier than a previously
	// accepted time.
	ErrClockWentBack = errors.New("canary: clock went backwards")
	// ErrIllegalState is reported when the operation is not allowed in the
	// current state.
	ErrIllegalState = errors.New("canary: operation not allowed in current state")
)

// Phase is the lifecycle state of a Splitter.
type Phase int

const (
	// PhaseNotStarted: no release in progress; ratio is 0.
	PhaseNotStarted Phase = iota
	// PhaseRunning: a gray stage is active.
	PhaseRunning
	// PhaseCompleted: the last stage passed evaluation; ratio stays final.
	PhaseCompleted
	// PhaseRolledBack: ratio is 0; only Reset can leave this state.
	PhaseRolledBack
)

// Version is the routing decision target.
type Version int

const (
	// VersionStable routes to the stable (baseline) implementation.
	VersionStable Version = iota
	// VersionGray routes to the gray (canary) implementation.
	VersionGray
)

// RouteSource explains why a routing decision was made.
type RouteSource int

const (
	// SourceRollback: rollback state forces stable; highest priority.
	SourceRollback RouteSource = iota
	// SourceSticky: an unexpired sticky record was reused.
	SourceSticky
	// SourceRatio: decided by bucket position against the current ratio.
	SourceRatio
)

// Config configures a Splitter. Ratios are strictly increasing permyriad
// values in [1, Basis].
type Config struct {
	// Stages are the gray ratios per stage, strictly increasing, 1..10000.
	Stages []int
	// MinDwell is the minimum residence time D of every stage, in ms.
	MinDwell int64
	// MinGrayRequests is the minimum gray sample size G for evaluation.
	MinGrayRequests int
	// ErrorRateTolerance is the tolerated error-rate slack T, permyriad.
	ErrorRateTolerance int
	// MaxConsecutiveFails is the failure streak limit F that triggers rollback.
	MaxConsecutiveFails int
	// StickyTTL is the sticky-session lifetime L, in ms ([enter, enter+L)).
	StickyTTL int64
	// MaxSticky is the maximum number P of sticky records (LRU eviction).
	MaxSticky int
}

func (c Config) validate() error {
	if len(c.Stages) == 0 {
		return ErrInvalidArgument
	}
	prev := 0
	for _, ratio := range c.Stages {
		if ratio < 1 || ratio > Basis || ratio <= prev {
			return ErrInvalidArgument
		}
		prev = ratio
	}
	if c.MinDwell < 0 ||
		c.MinGrayRequests < 0 ||
		c.ErrorRateTolerance < 0 || c.ErrorRateTolerance > Basis ||
		c.MaxConsecutiveFails < 1 ||
		c.StickyTTL < 0 ||
		c.MaxSticky < 0 {
		return ErrInvalidArgument
	}
	return nil
}

// RouteResult is the outcome of Route.
type RouteResult struct {
	Version Version
	Source  RouteSource
}

// EvalOutcome classifies the result of Evaluate.
type EvalOutcome int

const (
	// EvalNotRunning: the splitter is not in the running phase.
	EvalNotRunning EvalOutcome = iota
	// EvalDwellNotMet: the current stage has not resided for D ms yet.
	EvalDwellNotMet
	// EvalInsufficientSamples: fewer than G gray requests observed.
	EvalInsufficientSamples
	// EvalPassed: the stage passed; stage advanced or release completed.
	EvalPassed
	// EvalFailed: the stage failed; streak incremented, possibly rolled back.
	EvalFailed
)

// EvalResult is the outcome of Evaluate.
type EvalResult struct {
	Outcome     EvalOutcome
	RolledBack  bool
	Advanced    bool
	Stage       int
	FailStreak  int
	GrayTotal   int
	GrayFail    int
	StableTotal int
	StableFail  int
}

// stickyEntry is one LRU-tracked sticky record.
type stickyEntry struct {
	id         string
	version    Version
	lastRouted int64
}

// evalWindow holds the current stage's observation counters.
type evalWindow struct {
	grayTotal   int
	grayFail    int
	stableTotal int
	stableFail  int
}

func (w *evalWindow) reset() {
	*w = evalWindow{}
}

// Splitter is the gray-release traffic splitter.
type Splitter struct {
	mu sync.Mutex

	cfg Config

	phase     Phase
	stage     int   // zero-based; meaningful while running
	enteredAt int64 // entry time of the current stage (running)
	lastNow   int64 // clock anchor; every accepted call must be >= this

	failStreak int
	window     evalWindow

	// sticky holds at most cfg.MaxSticky records; list front is most recently
	// routed, list back is the eviction victim.
	sticky *list.List
	index  map[string]*list.Element
}

// New constructs a Splitter from a validated Config.
func New(cfg Config) (*Splitter, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Splitter{
		cfg:    cfg,
		sticky: list.New(),
		index:  make(map[string]*list.Element),
	}, nil
}

// placementHash maps an id to its stable 64-bit hash (FNV-1a).
func placementHash(id string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(id))
	return mix64(h.Sum64())
}

// mix64 is the SplitMix64 avalanche finalizer. FNV-1a alone has weak high-bit
// diffusion for structured (e.g. sequential) ids, so its output is finalized
// before bucket mapping; the result is bijective and therefore keeps the hash
// collision properties while spreading all bits uniformly.
func mix64(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// Placement returns the stable bucket position 0..Basis-1 of id. The position
// is a pure function of id: identical ids always map to the same position and,
// over many distinct ids, positions are approximately uniform.
//
// Mapping takes the high word of hash*Basis (math/bits.Mul64) rather than
// hash%Basis: bucket i then covers exactly [i/Basis,(i+1)/Basis) of the hash
// space, so every bucket has equal probability with no modulo bias.
func Placement(id string) int {
	hi, _ := bits.Mul64(placementHash(id), uint64(Basis))
	return int(hi)
}

// PlacementOf returns the stable bucket position in this splitter's id space.
func (s *Splitter) PlacementOf(id string) int { return Placement(id) }

// Phase reports the current lifecycle phase.
func (s *Splitter) Phase() Phase {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.phase
}

// Stage reports the zero-based current stage.
func (s *Splitter) Stage() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stage
}

// Ratio reports the effective gray ratio (permyriad).
func (s *Splitter) Ratio() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ratioLocked()
}

func (s *Splitter) ratioLocked() int {
	switch s.phase {
	case PhaseRunning:
		return s.cfg.Stages[s.stage]
	case PhaseCompleted:
		return s.cfg.Stages[len(s.cfg.Stages)-1]
	default:
		return 0
	}
}

// FailStreak reports the current consecutive-failure count.
func (s *Splitter) FailStreak() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failStreak
}

// StickyCount reports the number of retained sticky records.
func (s *Splitter) StickyCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sticky.Len()
}

// checkClock rejects times earlier than the last accepted time.
func (s *Splitter) checkClock(now int64) error {
	if now < s.lastNow {
		return ErrClockWentBack
	}
	return nil
}

func (s *Splitter) clearStickyLocked() {
	s.sticky.Init()
	s.index = make(map[string]*list.Element)
}

// upsertSticky records that id was routed to version at time now and refreshes
// its LRU position, evicting the least recently routed record when the cap is
// exceeded.
func (s *Splitter) upsertSticky(id string, version Version, now int64) {
	if elem, ok := s.index[id]; ok {
		entry := elem.Value.(*stickyEntry)
		entry.version = version
		entry.lastRouted = now
		s.sticky.MoveToFront(elem)
		return
	}
	elem := s.sticky.PushFront(&stickyEntry{id: id, version: version, lastRouted: now})
	s.index[id] = elem
	for s.sticky.Len() > s.cfg.MaxSticky {
		s.evictOldestLocked()
	}
}

// evictOldestLocked removes the least recently routed record. Ties on the
// route time (possible when multiple requests share an injected timestamp)
// are broken by the smaller id, so the result is fully deterministic.
func (s *Splitter) evictOldestLocked() {
	var victim *list.Element
	for e := s.sticky.Back(); e != nil; e = e.Prev() {
		candidate := e.Value.(*stickyEntry)
		if victim == nil {
			victim = e
			continue
		}
		current := victim.Value.(*stickyEntry)
		if candidate.lastRouted < current.lastRouted ||
			(candidate.lastRouted == current.lastRouted && candidate.id < current.id) {
			victim = e
		}
	}
	if victim == nil {
		return
	}
	s.sticky.Remove(victim)
	delete(s.index, victim.Value.(*stickyEntry).id)
}

func (s *Splitter) resetAllLocked(now int64) {
	s.phase = PhaseNotStarted
	s.stage = 0
	s.enteredAt = 0
	s.lastNow = now
	s.failStreak = 0
	s.window.reset()
	s.clearStickyLocked()
}

// Start enters stage 0; valid only from NotStarted.
func (s *Splitter) Start(now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	if s.phase != PhaseNotStarted {
		return ErrIllegalState
	}
	s.phase = PhaseRunning
	s.stage = 0
	s.enteredAt = now
	s.lastNow = now
	s.failStreak = 0
	s.window.reset()
	return nil
}

// Reset clears every non-config field and returns to NotStarted. It is allowed
// from every phase.
func (s *Splitter) Reset(now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.resetAllLocked(now)
	return nil
}

// Demote moves one stage back; valid only while running past stage 0. The
// window and failure streak are cleared and the dwell clock restarts; sticky
// records are preserved.
func (s *Splitter) Demote(now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	if s.phase != PhaseRunning || s.stage == 0 {
		return ErrIllegalState
	}
	s.stage--
	s.enteredAt = now
	s.lastNow = now
	s.failStreak = 0
	s.window.reset()
	return nil
}

// Route decides the target version of id at time now.
func (s *Splitter) Route(id string, now int64) (RouteResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		return RouteResult{}, ErrInvalidArgument
	}
	if err := s.checkClock(now); err != nil {
		return RouteResult{}, err
	}
	s.lastNow = now

	// Priority 1: rollback forces stable; sticky records are neither read nor
	// written.
	if s.phase == PhaseRolledBack {
		return RouteResult{Version: VersionStable, Source: SourceRollback}, nil
	}

	// Priority 2: an unexpired sticky record in [lastRouted, lastRouted+L).
	if elem, ok := s.index[id]; ok {
		entry := elem.Value.(*stickyEntry)
		if now-entry.lastRouted < s.cfg.StickyTTL {
			entry.lastRouted = now
			s.sticky.MoveToFront(elem)
			return RouteResult{Version: entry.version, Source: SourceSticky}, nil
		}
		// Expired: drop before re-deciding by ratio so a stale record cannot
		// mask a changed ratio and capacity accounting stays exact.
		s.sticky.Remove(elem)
		delete(s.index, id)
	}

	// Priority 3: bucket position against the current ratio.
	version := VersionStable
	if Placement(id) < s.ratioLocked() {
		version = VersionGray
	}
	s.upsertSticky(id, version, now)
	return RouteResult{Version: version, Source: SourceRatio}, nil
}

// Observe records one request outcome for version v at time now. Outside the
// running phase the observation is ignored without error.
func (s *Splitter) Observe(version Version, success bool, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if version != VersionStable && version != VersionGray {
		return ErrInvalidArgument
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.lastNow = now
	if s.phase != PhaseRunning {
		return nil
	}
	switch version {
	case VersionGray:
		s.window.grayTotal++
		if !success {
			s.window.grayFail++
		}
	case VersionStable:
		s.window.stableTotal++
		if !success {
			s.window.stableFail++
		}
	}
	return nil
}

// Evaluate runs the periodic metric gate at time now.
func (s *Splitter) Evaluate(now int64) (EvalResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return EvalResult{}, err
	}
	s.lastNow = now

	res := EvalResult{
		Stage:       s.stage,
		FailStreak:  s.failStreak,
		GrayTotal:   s.window.grayTotal,
		GrayFail:    s.window.grayFail,
		StableTotal: s.window.stableTotal,
		StableFail:  s.window.stableFail,
	}
	if s.phase != PhaseRunning {
		res.Outcome = EvalNotRunning
		return res, nil
	}
	if now-s.enteredAt < s.cfg.MinDwell {
		res.Outcome = EvalDwellNotMet
		return res, nil
	}
	if s.window.grayTotal < s.cfg.MinGrayRequests {
		res.Outcome = EvalInsufficientSamples
		return res, nil
	}

	if s.gatePassedLocked() {
		s.failStreak = 0
		s.window.reset()
		res.Outcome = EvalPassed
		res.FailStreak = 0
		if s.stage == len(s.cfg.Stages)-1 {
			s.phase = PhaseCompleted
		} else {
			s.stage++
			s.enteredAt = now
			res.Advanced = true
		}
		res.Stage = s.stage
		return res, nil
	}

	s.failStreak++
	res.Outcome = EvalFailed
	res.FailStreak = s.failStreak
	if s.failStreak >= s.cfg.MaxConsecutiveFails {
		s.phase = PhaseRolledBack
		s.window.reset()
		s.clearStickyLocked()
		res.RolledBack = true
		return res, nil
	}
	s.enteredAt = now
	s.window.reset()
	return res, nil
}

// gatePassedLocked compares gray and stable error rates exactly, without
// floating point arithmetic:
//
//	grayFail/grayTotal <= stableFail/stableTotal + T/Basis
//
// A stable side with zero requests is treated as error rate 0. Every term is
// multiplied out and compared with arbitrary-precision integers, so boundary
// cases (including exact equality) are never misjudged by rounding.
func (s *Splitter) gatePassedLocked() bool {
	basis := big.NewInt(int64(Basis))
	grayTotal := big.NewInt(int64(s.window.grayTotal))
	grayFail := big.NewInt(int64(s.window.grayFail))

	// RHS as one fraction: (stableFail*Basis + T*stableTotal)/(stableTotal*Basis).
	// With stableTotal == 0 the stable error rate is 0, so the RHS is T/Basis.
	var rhsDenom, rhsNumer *big.Int
	if s.window.stableTotal == 0 {
		rhsDenom = basis
		rhsNumer = big.NewInt(int64(s.cfg.ErrorRateTolerance))
	} else {
		stableTotal := big.NewInt(int64(s.window.stableTotal))
		rhsDenom = new(big.Int).Mul(stableTotal, basis)
		rhsNumer = new(big.Int).Mul(big.NewInt(int64(s.window.stableFail)), basis)
		rhsNumer.Add(rhsNumer,
			new(big.Int).Mul(big.NewInt(int64(s.cfg.ErrorRateTolerance)), stableTotal))
	}

	lhs := new(big.Int).Mul(grayFail, rhsDenom)
	rhs := new(big.Int).Mul(grayTotal, rhsNumer)
	return lhs.Cmp(rhs) <= 0
}
