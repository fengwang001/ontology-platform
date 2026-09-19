package ontology

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInconsistent is returned by every write path once the store has been
// marked inconsistent by a failed compensation step.
var ErrInconsistent = errors.New("ontology: store is inconsistent, writes rejected")

// ParamIssueKind classifies a single parameter problem so callers can
// distinguish the three categories programmatically.
type ParamIssueKind int

const (
	// IssueMissing: a required parameter was not supplied.
	IssueMissing ParamIssueKind = iota
	// IssueType: a supplied parameter has the wrong type.
	IssueType
	// IssueUnknown: a parameter was supplied that the schema does not declare.
	IssueUnknown
)

func (k ParamIssueKind) String() string {
	switch k {
	case IssueMissing:
		return "missing"
	case IssueType:
		return "type"
	case IssueUnknown:
		return "unknown"
	}
	return "?"
}

// ParamIssue describes one parameter problem found during validation.
type ParamIssue struct {
	Kind ParamIssueKind
	Name string
	Want ParamType
	Got  string
}

// ParamErrors aggregates every parameter problem of one call; validation
// never stops at the first issue.
type ParamErrors struct {
	Action string
	Issues []ParamIssue
}

func (e *ParamErrors) Error() string {
	parts := make([]string, len(e.Issues))
	for i, is := range e.Issues {
		switch is.Kind {
		case IssueMissing:
			parts[i] = fmt.Sprintf("missing required param %q", is.Name)
		case IssueType:
			parts[i] = fmt.Sprintf("param %q: want %s, got %s", is.Name, is.Want, is.Got)
		case IssueUnknown:
			parts[i] = fmt.Sprintf("unknown param %q", is.Name)
		}
	}
	return fmt.Sprintf("action %q: invalid params: %s", e.Action, strings.Join(parts, "; "))
}

// HookRejectedError reports that the hook at Index refused the action.
type HookRejectedError struct {
	Action string
	Index  int
	Reason string
}

func (e *HookRejectedError) Error() string {
	return fmt.Sprintf("action %q: hook #%d rejected: %s", e.Action, e.Index, e.Reason)
}

// HookViolationError reports that the hook at Index attempted to write to
// the store; hooks are read-only and any write fails the whole action.
type HookViolationError struct {
	Action string
	Index  int
}

func (e *HookViolationError) Error() string {
	return fmt.Sprintf("action %q: hook #%d attempted a forbidden write", e.Action, e.Index)
}

// RecursionError reports a direct or indirect recursive action call.
type RecursionError struct {
	Chain []string
}

func (e *RecursionError) Error() string {
	return "recursive action call rejected: " + strings.Join(e.Chain, " -> ")
}

// DepthError reports that the nesting depth limit was exceeded.
type DepthError struct {
	Chain []string
	Limit int
}

func (e *DepthError) Error() string {
	return fmt.Sprintf("nesting depth limit %d exceeded: %s",
		e.Limit, strings.Join(e.Chain, " -> "))
}
