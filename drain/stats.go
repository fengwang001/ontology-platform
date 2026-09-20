package drain

// Stats is a point-in-time snapshot of the gate's counters.
type Stats struct {
	// Admitted is the cumulative number of successful Enter calls.
	Admitted int
	// Rejected is the cumulative number of Enter calls refused
	// because shutdown had already started.
	Rejected int
	// InFlight is the number of admitted requests not yet released.
	InFlight int
	// Done reports whether the shutdown procedure has finished,
	// either by draining to zero or by timing out.
	Done bool
}
