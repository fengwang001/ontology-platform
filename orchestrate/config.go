package orchestrate

import (
	"ontology/policy"
)

// Config carries resource caps, retry policy and the injected clock.
// Zero means "no limit" for the step/journal caps; MaxConcurrency zero
// means "one goroutine per ready step".
type Config struct {
	MaxSteps          int
	MaxJournalRecords int
	MaxConcurrency    int
	Policy            policy.Policy
	Clock             policy.Clock
}

func (c Config) validate() error {
	if c.MaxSteps < 0 || c.MaxJournalRecords < 0 {
		return ErrStepLimit
	}
	if c.MaxConcurrency < 0 {
		return ErrConcurrencyLimit
	}
	return nil
}

// CheckConcurrency validates a standalone concurrency cap value.
func CheckConcurrency(n int) error {
	if n < 0 {
		return ErrConcurrencyLimit
	}
	return nil
}
