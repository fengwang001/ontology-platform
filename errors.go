package projection

import "fmt"

// ProjectionError describes a projection that would violate structural
// constraints. Callers can inspect Field, RuleRaw and SourceRuleRaw to
// pinpoint exactly which attribute was removed by which rule.
type ProjectionError struct {
	// Field is the dotted attribute path involved.
	Field string
	// Kind distinguishes required-field violations from dependency violations.
	Kind ErrorKind
	// RuleRaw is the original rule text that hid the field (or its source).
	RuleRaw string
	// SourceField / SourceRuleRaw apply to dependency violations only.
	SourceField   string
	SourceRuleRaw string
}

// ErrorKind classifies a ProjectionError.
type ErrorKind int

const (
	// ErrorRequiredHidden: a required attribute was cut by a rule.
	ErrorRequiredHidden ErrorKind = iota
	// ErrorDependencyBroken: a computed field's source was hidden while the
	// field stayed visible, and the policy is DependencyError.
	ErrorDependencyBroken
)

func (e *ProjectionError) Error() string {
	switch e.Kind {
	case ErrorRequiredHidden:
		return fmt.Sprintf("projection violates schema: required field %q hidden by rule %q", e.Field, e.RuleRaw)
	case ErrorDependencyBroken:
		return fmt.Sprintf("projection violates schema: field %q is visible but its declared source %q is hidden by rule %q",
			e.Field, e.SourceField, e.SourceRuleRaw)
	default:
		return "projection violates schema"
	}
}

// AsProjectionError unwraps err to a *ProjectionError.
func AsProjectionError(err error) (*ProjectionError, bool) {
	pe, ok := err.(*ProjectionError)
	return pe, ok
}
