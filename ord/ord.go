// Package ord defines the Ricart–Agrawala total order on requests:
// smaller logical timestamp wins; ties are broken by smaller pid.
package ord

// Ticket is a request's position in the total order: (logical clock, pid).
type Ticket struct {
	TS  int
	PID int
}

// Less reports whether a precedes b in the total order. Pids are unique,
// so equal timestamps are always broken by the pid comparison.
func Less(a, b Ticket) bool {
	if a.TS != b.TS {
		return a.TS < b.TS
	}
	return a.PID < b.PID
}
