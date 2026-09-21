package mux

import "errors"

var (
	// ErrDuplicateID is returned by Register and Wait when the id is
	// already registered and has not completed yet.
	ErrDuplicateID = errors.New("mux: duplicate id")
	// ErrTimedOut is the result of a waiter whose deadline passed.
	ErrTimedOut = errors.New("mux: timed out")
	// ErrClosed is returned by operations on a closed Mux and is the
	// result of waiters interrupted by Close.
	ErrClosed = errors.New("mux: closed")
)
