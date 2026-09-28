// Package tokenroute implements session-consistency token routing.
//
// Writes against the primary produce monotonically increasing sequence
// numbers. Each read-only replica tracks the highest sequence number it has
// applied. A session holds a token; after a write the token becomes the
// maximum of its current value and the new sequence number. Reads are routed
// only to replicas whose applied number is at least the session token.
package tokenroute

// Reason is a machine-readable, distinguishable cause of a rejected call.
type Reason string

const (
	// ReasonUnknownSession means the session ID has not been opened.
	ReasonUnknownSession Reason = "unknown_session"
	// ReasonUnknownReplica means the replica name is not registered.
	ReasonUnknownReplica Reason = "unknown_replica"
	// ReasonStaleReplica means no replica has applied up to the session token.
	ReasonStaleReplica Reason = "stale_replica"
	// ReasonAdvanceRollback means the proposed applied number is below the
	// replica's current applied number.
	ReasonAdvanceRollback Reason = "advance_rollback"
	// ReasonAdvanceAhead means the proposed applied number is greater than
	// the primary's latest sequence number.
	ReasonAdvanceAhead Reason = "advance_ahead"
	// ReasonInvalidInput means an argument was malformed (blank id/name).
	ReasonInvalidInput Reason = "invalid_input"
)

// WriteResult is the outcome of a write against the primary.
type WriteResult struct {
	Session string
	Seq     int64
	Token   int64
}

// ReadResult is the outcome of routing a read to a replica.
type ReadResult struct {
	Session string
	Replica string
	Applied int64
	Token   int64
}

// AdvanceResult is the outcome of advancing a replica's applied watermark.
type AdvanceResult struct {
	Replica string
	Applied int64
}

// RejectError reports why an operation was refused. Rejected operations never
// mutate the primary sequence number, replica watermarks, or session tokens.
type RejectError struct {
	Reason Reason
	Op     string
	Detail string
}

func (e *RejectError) Error() string { return "" }

// Router is a concurrency-safe session-consistency token router.
type Router struct{}

// New creates a Router whose replicas are registered with applied watermark 0.
func New(replicaNames []string) *Router { return &Router{} }

// RegisterReplica adds a new replica with applied watermark 0.
func (r *Router) RegisterReplica(name string) error { return nil }

// OpenSession registers a session carrying token 0.
func (r *Router) OpenSession(id string) error { return nil }

// Write applies a write to the primary and advances the session token.
func (r *Router) Write(session string) (WriteResult, error) {
	return WriteResult{}, nil
}

// Read routes a read for the session to the freshest eligible, least-progress
// replica, and advances the session token to the observed watermark.
func (r *Router) Read(session string) (ReadResult, error) {
	return ReadResult{}, nil
}

// Advance sets a replica's applied watermark. Rollbacks and numbers ahead of
// the primary are rejected.
func (r *Router) Advance(replica string, applied int64) (AdvanceResult, error) {
	return AdvanceResult{}, nil
}

// snapshot returns a point-in-time copy of all state for tests.
type snapshot struct{}

func (r *Router) snapshotForTest() snapshot { return snapshot{} }
