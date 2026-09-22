package view

import "errors"

// ErrInjectedCrash is the sentinel crash hooks return in tests to model a
// process failure at a specific maintenance phase.
var ErrInjectedCrash = errors.New("view: injected crash")
