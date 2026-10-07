// Package payledger implements a payment pre-authorization ledger with
// incremental authorizations, partial and multi captures, reversals and
// refunds.
//
// Error precedence (only the first matching error is reported):
//
//	ErrInvalidParam > ErrClockRegression > ErrDuplicateAuthID (authorize only)
//	> ErrAuthNotFound > ErrAuthTerminated > ErrOverTolerance > ErrInsufficientFunds
//
// Refunds use a dedicated chain:
//
//	ErrInvalidParam > ErrClockRegression > ErrAuthNotFound > ErrRefundExceeds
package payledger

import "errors"

var (
	// ErrInvalidParam is returned when an argument is malformed
	// (empty id, non-positive amount, negative day, negative limit).
	ErrInvalidParam = errors.New("payledger: invalid parameter")
	// ErrClockRegression is returned when now is smaller than the now of
	// the last accepted operation's day.
	ErrClockRegression = errors.New("payledger: clock regression")
	// ErrDuplicateAuthID is returned when an authorization id has already
	// been used by a previously accepted authorize call, even if that
	// authorization has since terminated.
	ErrDuplicateAuthID = errors.New("payledger: duplicate authorization id")
	// ErrAccountNotFound is returned when the referenced card account
	// does not exist.
	ErrAccountNotFound = errors.New("payledger: account not found")
	// ErrAccountExists is returned when creating an account whose id is
	// already taken.
	ErrAccountExists = errors.New("payledger: account already exists")
	// ErrAuthNotFound is returned when the referenced authorization does
	// not exist.
	ErrAuthNotFound = errors.New("payledger: authorization not found")
	// ErrAuthTerminated is returned when operating on an authorization
	// that is reversed, final-captured, or expired at the given now.
	ErrAuthTerminated = errors.New("payledger: authorization already terminated")
	// ErrOverTolerance is returned when a capture would push the
	// cumulative captured amount above the authorized amount inflated by
	// the tolerance basis points (uplift rounded down).
	ErrOverTolerance = errors.New("payledger: capture exceeds tolerance")
	// ErrInsufficientFunds is returned when the available credit of the
	// account cannot cover the requested amount.
	ErrInsufficientFunds = errors.New("payledger: insufficient available credit")
	// ErrRefundExceeds is returned when a refund exceeds the cumulative
	// captured amount minus the already refunded amount.
	ErrRefundExceeds = errors.New("payledger: refund exceeds refundable amount")
)
