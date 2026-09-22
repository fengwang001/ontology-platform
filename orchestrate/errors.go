package orchestrate

import (
	"errors"

	"ontology/journal"
)

// The three resource-limit errors are mutually distinct and decidable via
// errors.Is. Every rejection leaves previously observable state untouched.
var (
	// ErrStepLimit: registering would exceed MaxSteps.
	ErrStepLimit = errors.New("orchestrate: step count limit exceeded")
	// ErrJournalLimit: appending would exceed MaxJournalRecords.
	ErrJournalLimit = errors.New("orchestrate: journal length limit exceeded")
	// ErrConcurrencyLimit: MaxConcurrency is outside the permitted range.
	ErrConcurrencyLimit = errors.New("orchestrate: concurrency limit out of range")
	// ErrUnregistered means the graph references a step with no action.
	ErrUnregistered = errors.New("orchestrate: step has no registered action")
	// ErrAlreadyTerminal means Run was called after a terminal outcome.
	ErrAlreadyTerminal = errors.New("orchestrate: run already finished")
)

// mapJournalError translates a journal-layer error without leaking state.
func mapJournalError(err error) error {
	if errors.Is(err, journal.ErrLimit) {
		return ErrJournalLimit
	}
	return err
}

// CrashedError reports that a configured crash hook terminated the run.
// The journal image already contains exactly the bytes the hook allowed;
// callers recover by constructing a fresh orchestrator and calling Recover.
type CrashedError struct {
	Seq   int
	Point journal.CrashPoint
}

func (e *CrashedError) Error() string {
	return "orchestrate: simulated crash at record " + itoa(e.Seq) + " " + e.Point.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [24]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
