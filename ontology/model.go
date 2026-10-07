// Package ontology provides an ontology link graph with a three-state
// reachability decider that separates object existence permissions from
// link traversal permissions.
package ontology

// Direction of a link type.
type Direction int

const (
	Unidirectional Direction = iota + 1
	Bidirectional
)

func (d Direction) Valid() bool { return d == Unidirectional || d == Bidirectional }

// ObjectType is a named object type.
type ObjectType struct {
	Name string
}

// LinkType is a named link type with a direction.
type LinkType struct {
	Name      string
	Direction Direction
}

// Object is an object instance belonging to an object type.
type Object struct {
	ID         string
	ObjectType string
}

// Link is a link instance connecting two objects and belonging to a link
// type. For bidirectional link types the instance is traversable in both
// directions; for unidirectional link types only Src -> Dst.
type Link struct {
	ID       string
	LinkType string
	Src      string
	Dst      string
}

// CallerID identifies the subject of a permission check.
type CallerID string

// Outcome is the three-state result of ReachableFrom.
type Outcome int

const (
	// Unreachable: no path exists at all in the ground-truth graph and both
	// endpoints are visible to the caller.
	Unreachable Outcome = iota
	// Reachable: at least one fully visible and fully traversable path exists.
	Reachable
	// RestrictedUnknown: existence permission is missing on an endpoint, or
	// every candidate path is truncated by a permission boundary.
	RestrictedUnknown
)

func (o Outcome) String() string {
	switch o {
	case Reachable:
		return "reachable"
	case Unreachable:
		return "unreachable"
	case RestrictedUnknown:
		return "restricted_unknown"
	default:
		return "invalid"
	}
}

// Reason classifies why an outcome was produced.
type Reason string

const (
	ReasonCertifiedPath       Reason = "certified_path"
	ReasonNoGroundTruthPath   Reason = "no_ground_truth_path"
	ReasonInvisibleEndpoint   Reason = "invisible_endpoint"
	ReasonCandidatesTruncated Reason = "candidates_truncated"
)
