package pctenc

// Mode selects the component of a URL whose allowed character set
// governs percent-encoding.
type Mode int

const (
	// Path applies the safe set for a path segment.
	Path Mode = iota
	// Query applies the safe set for a query key or value.
	Query
	// Fragment applies the safe set for a fragment.
	Fragment
)
