// Package insurance implements claim allocation and limit consumption
// for overlapping insurance policies (double insurance).
package insurance

import "fmt"

// ClauseType is the policy clause type.
type ClauseType int

const (
	// ClauseNormal participates in the first-layer pro-rata allocation.
	ClauseNormal ClauseType = iota
	// ClauseExcess only pays when a loss remains after normal policies pay.
	ClauseExcess
)

// String returns a human-readable name of the clause type.
func (c ClauseType) String() string {
	switch c {
	case ClauseNormal:
		return "normal"
	case ClauseExcess:
		return "excess"
	default:
		return fmt.Sprintf("clause(%d)", int(c))
	}
}

// Policy describes an insurance policy. Dates are integer days and both
// Effective and Expiry are inclusive.
type Policy struct {
	ID         string
	Subject    string
	SumInsured int64 // must be positive
	Deductible int64 // per-accident deductible, non-negative
	Effective  int64 // first covered day, inclusive
	Expiry     int64 // last covered day, inclusive
	Insurer    string
	Clause     ClauseType
}

// Accident describes a loss event.
type Accident struct {
	ID      string
	Subject string
	Date    int64 // occurrence day, non-negative
	Loss    int64 // loss amount, non-negative
}

// Payout is a single policy's payment for one accident.
type Payout struct {
	PolicyID string
	Amount   int64
}

// AccidentResult is the settlement outcome of one accident.
type AccidentResult struct {
	AccidentID string
	Seq        int      // global registration sequence, starting at 0
	Loss       int64    // current loss amount (after corrections)
	TotalPaid  int64    // sum of all payouts, never exceeds Loss
	Payouts    []Payout // all covering policies (including zeros), sorted by policy ID
}

// PolicyStatus is a snapshot of a policy's current state.
type PolicyStatus struct {
	Policy     Policy
	Seq        int   // policy registration sequence
	TotalPaid  int64 // cumulative paid amount
	Remaining  int64 // SumInsured - TotalPaid
	Cancelled  bool
	CancelDate int64 // cancellation effective day (meaningful only if Cancelled)
}

// ErrorKind classifies errors. Reported priority order:
// invalid param > duplicate id > not found > already cancelled.
type ErrorKind int

const (
	// ErrKindInvalidParam indicates an invalid parameter (highest priority).
	ErrKindInvalidParam ErrorKind = iota
	// ErrKindDuplicateID indicates a duplicated identifier.
	ErrKindDuplicateID
	// ErrKindNotFound indicates a missing accident or policy.
	ErrKindNotFound
	// ErrKindAlreadyCancelled indicates cancellation of a cancelled policy.
	ErrKindAlreadyCancelled
)

// String returns a human-readable name of the error kind.
func (k ErrorKind) String() string {
	switch k {
	case ErrKindInvalidParam:
		return "invalid-param"
	case ErrKindDuplicateID:
		return "duplicate-id"
	case ErrKindNotFound:
		return "not-found"
	case ErrKindAlreadyCancelled:
		return "already-cancelled"
	default:
		return fmt.Sprintf("kind(%d)", int(k))
	}
}

// Error is a classified error; callers can switch on Kind.
type Error struct {
	Kind ErrorKind
	Msg  string
}

// Error implements the error interface.
func (e *Error) Error() string {
	return fmt.Sprintf("insurance: %s: %s", e.Kind, e.Msg)
}

func newError(kind ErrorKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

// KindOf extracts the ErrorKind of an error produced by this package.
func KindOf(err error) (ErrorKind, bool) {
	if e, ok := err.(*Error); ok {
		return e.Kind, true
	}
	return 0, false
}
