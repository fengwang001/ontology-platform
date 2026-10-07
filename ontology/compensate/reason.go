// Package compensate implements action side-effect compensation (saga rollback)
// for the ontology platform.
//
// An action declares an ordered list of sub-operations. Each sub-operation
// takes effect in declaration order and, in the same indivisible processing
// unit, registers its inverse. When a later sub-operation is rejected or
// fails, all previously effective sub-operations are undone in strict reverse
// order. Inverse failures (including panics) never abort compensation and
// produce contamination markers on the involved object instances.
package compensate

import "fmt"

// ReasonClass classifies why an action or a compensation step did not succeed.
type ReasonClass int

const (
	// ReasonNone means no failure occurred.
	ReasonNone ReasonClass = iota
	// ReasonContaminated: an object instance is in the polluted state.
	ReasonContaminated
	// ReasonBusinessReject: a sub-operation's own business validation refused.
	ReasonBusinessReject
	// ReasonContention: a concurrent action contended the same object/link.
	ReasonContention
	// ReasonCompensationFailed: an inverse operation failed during compensation.
	ReasonCompensationFailed
)

// Failure records one rejected/failed point together with its reason.
type Failure struct {
	OpIndex int
	Class   ReasonClass
	Detail  string
}

// priorityRank is the fixed precedence used when one call touches several
// classes: contaminated > business reject > contention > compensation failed.
// The order is deliberately independent of whether compensation ran or
// succeeded.
var priorityRank = map[ReasonClass]int{
	ReasonNone:               -1,
	ReasonContaminated:       3,
	ReasonBusinessReject:     2,
	ReasonContention:         1,
	ReasonCompensationFailed: 0,
}

// HigherReason returns whichever of the two classes has higher fixed priority.
func HigherReason(a, b ReasonClass) ReasonClass {
	if priorityRank[a] >= priorityRank[b] {
		return a
	}
	return b
}

func (c ReasonClass) String() string {
	switch c {
	case ReasonNone:
		return "none"
	case ReasonContaminated:
		return "contaminated"
	case ReasonBusinessReject:
		return "business_reject"
	case ReasonContention:
		return "contention"
	case ReasonCompensationFailed:
		return "compensation_failed"
	default:
		return fmt.Sprintf("reason(%d)", int(c))
	}
}

func (f Failure) Error() string {
	return fmt.Sprintf("op#%d %s: %s", f.OpIndex, f.Class, f.Detail)
}

// BusinessError is a plain business validation rejection (ReasonBusinessReject).
type BusinessError struct{ Msg string }

func (e *BusinessError) Error() string { return e.Msg }

// ContentionError means a needed object/link resource was already locked by
// another action (ReasonContention).
type ContentionError struct{ Resource string }

func (e *ContentionError) Error() string {
	return "contention on " + e.Resource
}

// classify maps any error to its reason class.
func classify(err error) (ReasonClass, string) {
	if _, ok := err.(*ContentionError); ok {
		return ReasonContention, err.Error()
	}
	return ReasonBusinessReject, err.Error()
}
