// Package ontology contains a deterministic multi-tenant token-bucket limiter.
//
// All time is supplied by callers; this package never reads the wall clock.
// Tokens are stored in fixed-point units of one nanosecond-token, so fractional
// token credit from short intervals is retained instead of being rounded away.
package ontology
