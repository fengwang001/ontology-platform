package ontology

import (
	"errors"
	"sync"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrAlreadyExists   = errors.New("already exists")
	ErrPatternSyntax   = errors.New("pattern syntax")
	ErrNullableRepeat  = errors.New("nullable repeat")
	ErrProgramTooLarge = errors.New("program too large")
	ErrNotFound        = errors.New("pattern not registered")
	ErrClockRewind     = errors.New("clock moved backwards")
	ErrBanned          = errors.New("pattern is banned")
	ErrGlobalBudget    = errors.New("global budget exhausted")
)

const (
	OutcomeMatch         = "match"
	OutcomeNoMatch       = "no_match"
	OutcomeLocalLimited  = "local_limit"
	OutcomeGlobalLimited = "global_limit"
)

type Executor struct {
	mu              sync.Mutex
	localLimit      int64
	epochLength     int64
	globalSteps     int64
	banThreshold    int64
	baseBanDuration int64
	programLimit    int
	maxNow          int64
	currentEpoch    int64
	remaining       int64
	patterns        map[string]*registeredPattern
}

type registeredPattern struct {
	program     *program
	memoize     bool
	localMiss   int64
	banCount    int64
	bannedUntil int64
}

type MatchResult struct {
	Kind  string
	Start int
	End   int
	Steps int64
}

type StatusResult struct {
	ConsecutiveLocalLimits int64
	BanCount               int64
	BannedUntil            int64
	Banned                 bool
}

func NewExecutor(localLimit, epochLength, globalStepsPerEpoch, banThreshold, baseBanDuration int64, programLimit int) (*Executor, error) {
	if localLimit < 1 || localLimit > 1_000_000 ||
		epochLength < 1 || epochLength > 1_000_000_000 ||
		globalStepsPerEpoch < 1 || globalStepsPerEpoch > 1_000_000_000 ||
		banThreshold < 1 || banThreshold > 100 ||
		baseBanDuration < 1 || baseBanDuration > 1_000_000_000 ||
		programLimit < 1 || programLimit > 100_000 {
		return nil, ErrInvalidArgument
	}
	return &Executor{
		localLimit:      localLimit,
		epochLength:     epochLength,
		globalSteps:     globalStepsPerEpoch,
		banThreshold:    banThreshold,
		baseBanDuration: baseBanDuration,
		programLimit:    programLimit,
		remaining:       globalStepsPerEpoch,
		patterns:        make(map[string]*registeredPattern),
	}, nil
}

func (e *Executor) Register(id string, pattern []byte, memoize bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(id) == 0 || len(id) > 64 || len(pattern) < 1 || len(pattern) > 200 {
		return ErrInvalidArgument
	}
	if _, ok := e.patterns[id]; ok {
		return ErrAlreadyExists
	}
	root, err := parsePattern(pattern)
	if err != nil {
		return err
	}
	prog, err := compilePattern(root, e.programLimit)
	if err != nil {
		return err
	}
	e.patterns[id] = &registeredPattern{program: prog, memoize: memoize}
	return nil
}

func (e *Executor) Match(id string, input []byte, now int64) (MatchResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(id) == 0 || len(id) > 64 || len(input) > 65536 || now < 0 || now > 1_000_000_000_000_000 {
		return MatchResult{}, ErrInvalidArgument
	}
	pattern, ok := e.patterns[id]
	if !ok {
		return MatchResult{}, ErrNotFound
	}
	if now < e.maxNow {
		return MatchResult{}, ErrClockRewind
	}
	if now < pattern.bannedUntil {
		return MatchResult{}, ErrBanned
	}

	epoch := now / e.epochLength
	effectiveRemaining := e.remaining
	if epoch != e.currentEpoch {
		effectiveRemaining = e.globalSteps
	}
	if effectiveRemaining == 0 {
		return MatchResult{}, ErrGlobalBudget
	}

	limitKind := OutcomeGlobalLimited
	limit := effectiveRemaining
	if effectiveRemaining >= e.localLimit {
		limitKind = OutcomeLocalLimited
		limit = e.localLimit
	}

	outcome := runProgram(pattern.program, input, limit, pattern.memoize, limitKind)

	e.maxNow = now
	if epoch != e.currentEpoch {
		e.currentEpoch = epoch
		e.remaining = e.globalSteps
	}
	e.remaining -= outcome.steps

	result := MatchResult{
		Kind:  outcome.kind,
		Start: outcome.start,
		End:   outcome.end,
		Steps: outcome.steps,
	}

	switch outcome.kind {
	case OutcomeMatch, OutcomeNoMatch:
		pattern.localMiss = 0
	case OutcomeLocalLimited:
		pattern.localMiss++
		if pattern.localMiss >= e.banThreshold {
			pattern.banCount++
			multiplier := int64(1)
			if pattern.banCount >= 4 {
				multiplier = 8
			} else {
				multiplier = int64(1) << (pattern.banCount - 1)
			}
			pattern.bannedUntil = now + e.baseBanDuration*multiplier
			pattern.localMiss = 0
		}
	}

	return result, nil
}

func (e *Executor) Status(id string, now int64) (StatusResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(id) == 0 || len(id) > 64 || now < 0 || now > 1_000_000_000_000_000 {
		return StatusResult{}, ErrInvalidArgument
	}
	pattern, ok := e.patterns[id]
	if !ok {
		return StatusResult{}, ErrNotFound
	}
	return StatusResult{
		ConsecutiveLocalLimits: pattern.localMiss,
		BanCount:               pattern.banCount,
		BannedUntil:            pattern.bannedUntil,
		Banned:                 now < pattern.bannedUntil,
	}, nil
}
