package correlate

import "time"

// Token is the correlation handle handed back when a request is issued.
// ID is the externally visible correlation ID; Epoch is the generation tag
// that distinguishes successive generations that reuse the same ID. Callers
// must echo the whole token back with a reply; a reply carrying an old epoch
// for a reused ID is rejected as an orphan instead of completing the newer
// request.
type Token struct {
	// ID is the reusable correlation ID in [0, capacity).
	ID int
	// Epoch starts at 1 and increments each time the ID is allocated.
	Epoch uint64
}

// Status is the observable state of one ID as reported by Lookup.
type Status struct {
	// Waiting is false for unknown IDs and finished generations.
	Waiting bool
	// Epoch is the generation of the current in-flight request.
	Epoch uint64
	// Remaining is the time left until timeout; it is zero when not
	// waiting, and may be negative for a request the next sweep will expire.
	Remaining time.Duration
}
