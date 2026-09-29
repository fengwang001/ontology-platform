package matcher

import "errors"

// Distinguishable rejection reasons.
var (
	ErrDegenerateFullScan   = errors.New("matcher: pattern rejected: degenerate full scan (no node constraints)")
	ErrIsomorphicDuplicates = errors.New("matcher: pattern rejected: structure produces duplicate isomorphic matches")
	ErrInconsistentBinding  = errors.New("matcher: pattern rejected: variable bound to inconsistent node declarations")
)
