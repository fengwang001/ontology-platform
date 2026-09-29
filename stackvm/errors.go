package stackvm

import "fmt"

// Static rejection reasons. Any of these makes the whole submitted batch
// invalid: no program in the batch is executed and storage is untouched.
const (
	ReasonUnknownOpcode = "unknown opcode"
	ReasonMissingTarget = "call target does not exist"
	ReasonNegativeWords = "memory expansion word count is negative"
)

func (e *StaticError) Error() string {
	if e.PC >= 0 {
		return fmt.Sprintf("stackvm: static check failed for program %q at pc %d: %s",
			e.Program, e.PC, e.Reason)
	}
	return fmt.Sprintf("stackvm: static check failed for program %q: %s",
		e.Program, e.Reason)
}

// String returns a stable human-readable name for a failure cause.
func (c FailCause) String() string {
	switch c {
	case CauseNone:
		return "none"
	case CauseFuelExhausted:
		return "fuel exhausted"
	case CauseStackUnderflow:
		return "stack underflow"
	case CauseExplicitFail:
		return "explicit fail"
	case CauseDepthExceeded:
		return "call depth exceeded"
	default:
		return fmt.Sprintf("cause(%d)", uint8(c))
	}
}

// frameError marks a running frame as failed with a distinguishable cause.
type frameError struct {
	cause FailCause
}

func (e *frameError) Error() string {
	return "stackvm: frame failed: " + e.cause.String()
}
