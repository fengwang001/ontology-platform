// Package correlate ties correlation IDs to in-flight requests. A caller
// issues a request and receives a Token (an ID plus an opaque generation tag);
// replies carry that token back, possibly out of order and possibly late.
//
// The correlator is fully in-process and concurrency safe. It never starts
// timers of its own: timeouts are evaluated lazily against an injected clock
// (the now function given at construction) at the start of every mutating
// operation and every read. Because reads also perform that lazy sweep,
// merely observing the correlator can move waiting slots whose deadline the
// injected clock has already passed into the timed-out state; two reads at
// the same clock value always return identical results.
package correlate

import "errors"

// Deliver and Cancel return one of the errors below; use errors.Is to
// classify them. The three orphan classes are mutually distinct.
var (
	// ErrOrphanUnknown: the token's ID has never been allocated.
	ErrOrphanUnknown = errors.New("correlate: orphan reply, id never allocated")
	// ErrOrphanIdle: the ID is free and its last generation is finished;
	// the reply arrived after completion/timeout/cancel and before reuse.
	ErrOrphanIdle = errors.New("correlate: orphan reply, id idle after finished generation")
	// ErrOrphanStale: the ID is held by a newer generation, so this reply
	// belongs to a timed-out or canceled predecessor. It must never
	// complete the current generation.
	ErrOrphanStale = errors.New("correlate: orphan reply, id held by a newer generation")
	// ErrCapacity is returned by Issue when the in-flight limit is hit.
	ErrCapacity = errors.New("correlate: in-flight capacity reached")
	// ErrNotInFlight is returned by Cancel for an ID that is unknown,
	// already finished, or whose token is stale.
	ErrNotInFlight = errors.New("correlate: no in-flight request for id")
)
