package ontology

import (
	"errors"
	"fmt"
	"strings"
)

// ViolationKind classifies link-integrity failures so callers can
// distinguish them programmatically via IsViolation.
type ViolationKind int

const (
	// ViolationOneToOne: ONE_TO_ONE source already linked or target occupied.
	ViolationOneToOne ViolationKind = iota
	// ViolationOneToMany: ONE_TO_MANY target already belongs to another source.
	ViolationOneToMany
	// ViolationEndpointType: endpoint object type does not match the LinkType.
	ViolationEndpointType
	// ViolationEndpointNotFound: endpoint object does not exist.
	ViolationEndpointNotFound
	// ViolationDuplicateLink: the exact link already exists.
	ViolationDuplicateLink
)

func (k ViolationKind) String() string {
	switch k {
	case ViolationOneToOne:
		return "ONE_TO_ONE_VIOLATION"
	case ViolationOneToMany:
		return "ONE_TO_MANY_VIOLATION"
	case ViolationEndpointType:
		return "ENDPOINT_TYPE_MISMATCH"
	case ViolationEndpointNotFound:
		return "ENDPOINT_NOT_FOUND"
	case ViolationDuplicateLink:
		return "DUPLICATE_LINK"
	}
	return "UNKNOWN_VIOLATION"
}

// LinkError reports a single link-integrity violation with full context.
type LinkError struct {
	Kind     ViolationKind
	LinkType string
	Source   string
	Target   string
	Detail   string
}

func (e *LinkError) Error() string {
	return fmt.Sprintf("link violation %s on linkType %q (%s -> %s): %s",
		e.Kind, e.LinkType, e.Source, e.Target, e.Detail)
}

// IsViolation reports whether err (or any error it wraps) is a LinkError of
// the given kind. It sees through BatchError wrapping.
func IsViolation(err error, kind ViolationKind) bool {
	var le *LinkError
	if errors.As(err, &le) {
		return le.Kind == kind
	}
	return false
}

// MissingRequired describes one unsatisfied required side of a LinkType.
type MissingRequired struct {
	ObjectID string
	LinkType string
	Side     string // "source" or "target"
}

// RequiredError aggregates every missing required relation found by
// ValidateRequired in a single pass.
type RequiredError struct {
	Missing []MissingRequired
}

func (e *RequiredError) Error() string {
	parts := make([]string, len(e.Missing))
	for i, m := range e.Missing {
		parts[i] = fmt.Sprintf("object %q misses required %s side of linkType %q",
			m.ObjectID, m.Side, m.LinkType)
	}
	return "required link violations: " + strings.Join(parts, "; ")
}

// RestrictError reports that a cascade delete hit a RESTRICT link. Path is
// the full hop chain from the originally deleted object to the restricted
// link (the last step is the restricted link itself).
type RestrictError struct {
	Path     []PathStep
	LinkType string
	Source   string
	Target   string
}

func (e *RestrictError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "delete restricted by linkType %q (%s -> %s)",
		e.LinkType, e.Source, e.Target)
	if len(e.Path) > 0 {
		b.WriteString(" via path: ")
		for i, s := range e.Path {
			if i > 0 {
				b.WriteString(" -> ")
			}
			fmt.Fprintf(&b, "%s -[%s]- %s", s.From, s.LinkType, s.To)
		}
	}
	return b.String()
}

// BatchError identifies which operation inside a batch failed.
type BatchError struct {
	Index int
	Op    Op
	Err   error
}

func (e *BatchError) Error() string {
	return fmt.Sprintf("batch op %d (%s) failed: %v", e.Index, e.Op, e.Err)
}

func (e *BatchError) Unwrap() error { return e.Err }

// InvariantError reports a broken forward/reverse index mirror.
type InvariantError struct {
	LinkType string
	Side     string // "forward" or "reverse": the index holding the orphan entry
	Source   string
	Target   string
	Detail   string
}

func (e *InvariantError) Error() string {
	return fmt.Sprintf("index invariant broken on linkType %q (%s side, %s -> %s): %s",
		e.LinkType, e.Side, e.Source, e.Target, e.Detail)
}
