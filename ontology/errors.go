package ontology

// ErrorKind enumerates the distinguishable error categories produced by the
// gateway. Configuration errors and plain "no permission" outcomes are
// represented by distinct kinds so callers never confuse them.
type ErrorKind string

const (
	// KindObjectNotFound: the queried or mutated object type does not exist.
	KindObjectNotFound ErrorKind = "object_type_not_found"
	// KindPropagationCycle: the propagation-enabled link graph contains a
	// directed cycle, so the configuration is rejected as a whole.
	KindPropagationCycle ErrorKind = "propagation_cycle"
	// KindInvalidDepth: a link declares a negative depth or a depth above
	// the platform cap MaxPropagationDepth.
	KindInvalidDepth ErrorKind = "invalid_depth"
	// KindConflictingOverride: the same target is subject to two
	// irreconcilable override rules at once (Replace vs Block).
	KindConflictingOverride ErrorKind = "conflicting_override"
	// KindNoPermission: an ordinary, non-erroneous denial outcome.
	KindNoPermission ErrorKind = "no_permission"
)

// GatewayError is the typed error returned by gateway operations.
type GatewayError struct {
	Kind ErrorKind
	Msg  string
}

func (e *GatewayError) Error() string {
	return string(e.Kind) + ": " + e.Msg
}

func newError(kind ErrorKind, msg string) *GatewayError {
	return &GatewayError{Kind: kind, Msg: msg}
}
