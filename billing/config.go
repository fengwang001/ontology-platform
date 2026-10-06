// Package billing implements a property-fee billing service with late-fee
// accrual, tiered dunning and payment application.
package billing

import "fmt"

// ErrCode classifies rejected operations. Codes are declared in the fixed
// reporting order: only the first applicable error is reported.
type ErrCode int

const (
	ErrInvalidParam  ErrCode = iota // invalid parameters
	ErrClockRollback                // clock rollback
	ErrNotFound                     // household or bill does not exist
	ErrInvalidState                 // state does not allow the operation
	ErrAmount                       // amount out of range
)

func (c ErrCode) String() string {
	switch c {
	case ErrInvalidParam:
		return "invalid-param"
	case ErrClockRollback:
		return "clock-rollback"
	case ErrNotFound:
		return "not-found"
	case ErrInvalidState:
		return "invalid-state"
	case ErrAmount:
		return "amount-out-of-range"
	}
	return "unknown"
}

// Error is the single error type returned by all operations.
type Error struct {
	Code ErrCode
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("billing: %s: %s", e.Code, e.Msg) }

func newError(code ErrCode, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// Config holds the service parameters fixed at construction.
type Config struct {
	GraceDays  int    // G: grace days after the due day
	RateNum    int64  // daily late-fee rate on unpaid principal: RateNum/RateDen
	RateDen    int64  //
	CapNum     int64  // late-fee cap as a ratio of principal: CapNum/CapDen
	CapDen     int64  //
	Thresholds [3]int // overdue-day thresholds for dunning stages 1..3, strictly increasing
}

func (c Config) validate() error {
	if c.GraceDays < 0 {
		return newError(ErrInvalidParam, "grace days must be >= 0, got %d", c.GraceDays)
	}
	if c.RateNum <= 0 || c.RateDen <= 0 {
		return newError(ErrInvalidParam, "late-fee rate must be positive, got %d/%d", c.RateNum, c.RateDen)
	}
	if c.CapNum <= 0 || c.CapDen <= 0 {
		return newError(ErrInvalidParam, "late-fee cap must be positive, got %d/%d", c.CapNum, c.CapDen)
	}
	for i, t := range c.Thresholds {
		if t <= 0 {
			return newError(ErrInvalidParam, "stage threshold %d must be positive, got %d", i+1, t)
		}
		if i > 0 && t <= c.Thresholds[i-1] {
			return newError(ErrInvalidParam, "stage thresholds must be strictly increasing: %v", c.Thresholds)
		}
	}
	return nil
}

// den is the common denominator of late-fee units: 1 unit = 1/den yuan.
// Late fees accrue in integer units so sub-yuan fractions carry over.
func (c Config) den() int64 { return c.RateDen * c.CapDen }

// rateUnits is the daily accrual in units per yuan of unpaid principal.
func (c Config) rateUnits() int64 { return c.RateNum * c.CapDen }

// capUnits is the accrual cap in units for the given principal.
func (c Config) capUnits(principal int64) int64 { return principal * c.CapNum * c.RateDen }
