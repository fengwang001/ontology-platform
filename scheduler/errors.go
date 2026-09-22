package scheduler

import "errors"

// Sentinel errors; all are decidable via errors.Is.
var (
	ErrNegativeDelay   = errors.New("scheduler: negative delay")
	ErrInvalidAdvance  = errors.New("scheduler: advance ticks must be positive")
	ErrAdvanceTooLarge = errors.New("scheduler: advance exceeds max ticks per call")
	ErrTooManyTimers   = errors.New("scheduler: live timer limit reached")
	ErrDelayTooLarge   = errors.New("scheduler: delay exceeds max delay or wheel range")
	ErrTimerNotFound   = errors.New("scheduler: unknown timer handle")
	ErrTimerFired      = errors.New("scheduler: timer already fired")
	ErrTimerCancelled  = errors.New("scheduler: timer already cancelled")
	ErrInvalidConfig   = errors.New("scheduler: invalid config")
)
