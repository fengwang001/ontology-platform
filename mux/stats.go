package mux

// Stats is a point-in-time snapshot of the matcher's counters.
type Stats struct {
	// Pending is the number of requests currently waiting.
	Pending int
	// Orphans counts delivered responses nobody ever registered for.
	Orphans int
	// Late counts delivered responses whose id was registered before
	// but had already completed (timed out or closed).
	Late int
	// Delivered counts responses successfully handed to a waiter.
	Delivered int
}
