package bracket

import "errors"

// Construction errors.
var (
	// ErrInvalidN is returned when N is outside [2, 64].
	ErrInvalidN = errors.New("bracket: N must be between 2 and 64")
)

// Report rejection reasons, checked in this exact order.
var (
	ErrMatchNotFound = errors.New("bracket: match (r, i) does not exist")
	ErrByeMatch      = errors.New("bracket: first-round bye match cannot be reported")
	ErrAlreadyPlayed = errors.New("bracket: match already has a result")
	ErrNotReady      = errors.New("bracket: match is not ready (both slots must be filled)")
	ErrNotContestant = errors.New("bracket: w is not a contestant of this match")
)

// Correct-specific rejection reasons (checked after the shared ones above).
var (
	ErrNoResult       = errors.New("bracket: match has no result yet")
	ErrTechnicalMatch = errors.New("bracket: technically decided match cannot be corrected")
	ErrAlreadyWinner  = errors.New("bracket: w is already the current winner")
	ErrNextHasResult  = errors.New("bracket: the corresponding match in the next round already has a result")
)

// Withdraw rejection reasons.
var (
	ErrSeedOutOfRange  = errors.New("bracket: seed is outside 1..N")
	ErrAlreadyOut      = errors.New("bracket: seed has already withdrawn")
	ErrEliminated      = errors.New("bracket: seed has already lost a match")
	ErrChampionCrowned = errors.New("bracket: champion has already been decided")
)
