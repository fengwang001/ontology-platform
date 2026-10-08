// Package dtc implements a vehicle diagnostic trouble code (DTC)
// lifecycle manager: debounce, pending/confirmed/healed/auto-cleared
// transitions, warm-up cycle detection, a single freeze frame slot and
// tester clears. See DESIGN.md for the rationale of key decisions.
package dtc

import "fmt"

// ErrCode classifies rejected operations. The numeric order of the
// constants is also the priority order used when an event violates
// several rules at once: the violation with the smallest code is
// reported.
//
//	Priority: ErrInvalidParam > ErrRegression > ErrBadSequence >
//	          ErrUnknownDTC > ErrStateNotAllowed
type ErrCode int

const (
	// ErrInvalidParam indicates an illegal parameter (bad severity,
	// negative timestamp/odometer, unknown event kind, invalid
	// configuration, ...).
	ErrInvalidParam ErrCode = iota + 1
	// ErrRegression indicates that the event time or odometer is
	// smaller than the last accepted event's.
	ErrRegression
	// ErrBadSequence indicates an event that violates the required
	// ignition-on / events / ignition-off ordering.
	ErrBadSequence
	// ErrUnknownDTC indicates a reference to a DTC that was never
	// registered.
	ErrUnknownDTC
	// ErrStateNotAllowed indicates an operation that is not allowed
	// in the current state (tester clear while ignition is on).
	ErrStateNotAllowed
)

// Error is the single error type returned by this package. Use errors.As
// to inspect the Code.
type Error struct {
	Code ErrCode
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("dtc: %s: %s", e.Code, e.Msg) }

func (c ErrCode) String() string {
	switch c {
	case ErrInvalidParam:
		return "invalid parameter"
	case ErrRegression:
		return "time/odometer regression"
	case ErrBadSequence:
		return "illegal event order"
	case ErrUnknownDTC:
		return "unknown DTC"
	case ErrStateNotAllowed:
		return "state not allowed"
	}
	return "unknown error code"
}

func errf(code ErrCode, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}
