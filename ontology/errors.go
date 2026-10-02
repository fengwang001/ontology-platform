package ontology

// RejectKind is the distinguishable rejection category.
type RejectKind int

const (
	RejectInvalid RejectKind = iota
	RejectClockRollback
	RejectBadSignature
	RejectRevoked
	RejectCaveat
	RejectLimit
)

// CaveatProblem classifies the first failing caveat.
type CaveatProblem int

const (
	CaveatUnknown CaveatProblem = iota
	CaveatMalformed
	CaveatUnsatisfied
)

// Error carries the first-failing category plus locating information.
type Error struct {
	Kind    RejectKind
	Index   int           // 1-based caveat index; meaningful for RejectCaveat
	Problem CaveatProblem // meaningful for RejectCaveat
	Prefix  int           // revoked prefix length j; meaningful for RejectRevoked
}

func (e *Error) Error() string {
	switch e.Kind {
	case RejectInvalid:
		return "macaroon: invalid parameters"
	case RejectClockRollback:
		return "macaroon: clock rollback"
	case RejectBadSignature:
		return "macaroon: signature mismatch"
	case RejectRevoked:
		return "macaroon: revoked prefix"
	case RejectCaveat:
		return "macaroon: caveat not satisfied"
	case RejectLimit:
		return "macaroon: limit exceeded"
	default:
		return "macaroon: rejected"
	}
}
