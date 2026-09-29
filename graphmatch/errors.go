package graphmatch

import "fmt"

// RejectReason identifies why a query was rejected. Reasons are
// distinguishable so callers can react programmatically.
type RejectReason string

const (
	// ReasonDuplicateIsomorphicMatch means the result set would contain two
	// matches whose variables map to the same set of objects.
	ReasonDuplicateIsomorphicMatch RejectReason = "duplicate_isomorphic_match"
	// ReasonInconsistentBinding means the same variable would be bound to
	// different objects within one match.
	ReasonInconsistentBinding RejectReason = "inconsistent_binding"
	// ReasonFullSearchDegradation means the pattern has an unconstrained
	// variable, so matching would degrade to a full Cartesian search.
	ReasonFullSearchDegradation RejectReason = "full_search_degradation"
)

// RejectError reports a rejected query. A rejected query returns no partial
// results.
type RejectError struct {
	Reason  RejectReason
	Detail  string
	Pattern string
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("graphmatch: query rejected (%s): %s [pattern=%s]", e.Reason, e.Detail, e.Pattern)
}
