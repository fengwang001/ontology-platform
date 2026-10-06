// Package billing implements a property-fee billing service: bill
// generation, grace-period late-fee accrual with a cap, tiered dunning
// stages, ordered payment allocation with prepayment, disputes with
// rulings, and late-fee waivers.
//
// All mutating operations take an integer day "now" that must never go
// backwards. Errors are reported by code in a fixed precedence:
// ErrInvalidParam, ErrClockRollback, ErrNotFound, ErrInvalidState,
// ErrAmountOutOfRange; only the first applicable error is returned and a
// rejected operation changes nothing.
package billing

import "fmt"

// ErrCode classifies a rejected operation. The numeric order of the
// constants is the reporting precedence.
type ErrCode int

const (
	ErrInvalidParam     ErrCode = iota + 1 // malformed arguments
	ErrClockRollback                       // now < last accepted now
	ErrNotFound                            // unknown household or bill
	ErrInvalidState                        // closed bill, duplicate dispute, re-ruling, ...
	ErrAmountOutOfRange                    // non-positive payment, waiver above unpaid late fee
)

func (c ErrCode) String() string {
	switch c {
	case ErrInvalidParam:
		return "invalid parameter"
	case ErrClockRollback:
		return "clock rollback"
	case ErrNotFound:
		return "not found"
	case ErrInvalidState:
		return "invalid state"
	case ErrAmountOutOfRange:
		return "amount out of range"
	}
	return "unknown error"
}

// Error is the single error type returned by the service.
type Error struct {
	Code ErrCode
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Msg) }

func paramErr(format string, args ...any) *Error {
	return &Error{Code: ErrInvalidParam, Msg: fmt.Sprintf(format, args...)}
}
func clockErr(format string, args ...any) *Error {
	return &Error{Code: ErrClockRollback, Msg: fmt.Sprintf(format, args...)}
}
func notFoundErr(format string, args ...any) *Error {
	return &Error{Code: ErrNotFound, Msg: fmt.Sprintf(format, args...)}
}
func stateErr(format string, args ...any) *Error {
	return &Error{Code: ErrInvalidState, Msg: fmt.Sprintf(format, args...)}
}
func amountErr(format string, args ...any) *Error {
	return &Error{Code: ErrAmountOutOfRange, Msg: fmt.Sprintf(format, args...)}
}

// Dunning stages. A bill starts at StageNone and only moves forward.
const (
	StageNone     = 0
	StageReminder = 1
	StageWarning  = 2
	StageFinal    = 3 // full-payment-only stage
)

// Config holds the constructor parameters of a Service.
type Config struct {
	GraceDays int64 // days after the due day during which no late fee accrues
	RateNum   int64 // daily late-fee rate numerator, applied to unpaid principal
	RateDen   int64 // daily late-fee rate denominator
	CapNum    int64 // late-fee cap numerator, as a ratio of principal
	CapDen    int64 // late-fee cap denominator
	// StageThresholds[i] is the overdue-day count at which stage i+1 is
	// reached; strictly increasing.
	StageThresholds [3]int64
}

func (c Config) validate() error {
	if c.GraceDays < 0 {
		return paramErr("grace days must be >= 0, got %d", c.GraceDays)
	}
	if c.RateNum < 0 || c.RateDen <= 0 {
		return paramErr("invalid daily rate %d/%d", c.RateNum, c.RateDen)
	}
	if c.CapNum < 0 || c.CapDen <= 0 {
		return paramErr("invalid cap ratio %d/%d", c.CapNum, c.CapDen)
	}
	for i, th := range c.StageThresholds {
		if th < 0 {
			return paramErr("stage threshold %d is negative", th)
		}
		if i > 0 && th <= c.StageThresholds[i-1] {
			return paramErr("stage thresholds must be strictly increasing: %v", c.StageThresholds)
		}
	}
	return nil
}
