// Package meshauthz implements a service-mesh workload authorization
// policy evaluator with atomic, versioned policy-set publication.
//
// The package is split into small cooperating modules:
//
//   - types.go:     request, policy, decision and evidence data model.
//   - errors.go:    distinguishable, prioritized error categories.
//   - pattern.go:   exact/prefix/suffix/wildcard element matching.
//   - validate.go:  request and policy-set validation.
//   - match.go:     rule/condition matching against a request.
//   - snapshot.go:  immutable, namespace-indexed compiled policy set.
//   - store.go:     atomic versioned store and the decision algorithm.
package meshauthz

// Action is the effect of a policy when one of its rules matches.
type Action string

const (
	ActionAllow Action = "ALLOW"
	ActionDeny  Action = "DENY"
	ActionAudit Action = "AUDIT"
)

// valid reports whether a is one of the three legal actions.
func (a Action) valid() bool {
	switch a {
	case ActionAllow, ActionDeny, ActionAudit:
		return true
	}
	return false
}

// Decision is the final verdict of an evaluation.
type Decision string

const (
	DecisionAllow Decision = "ALLOW"
	DecisionDeny  Decision = "DENY"
)

// Request describes a single workload-to-workload call.
//
// Header names are case-insensitive and a name may carry multiple values.
type Request struct {
	SourceIdentity  string
	SourceNamespace string
	TargetNamespace string
	TargetLabels    map[string]string
	Method          string
	Path            string
	Port            int
	Headers         map[string][]string
}

// Source is the "from" part of a rule.
type Source struct {
	Identities    []string
	NotIdentities []string
	Namespaces    []string
	NotNamespaces []string
}

// Operation is the "to" part of a rule.
type Operation struct {
	Methods    []string
	NotMethods []string
	Paths      []string
	NotPaths   []string
	Ports      []int
	NotPorts   []int
}

// Condition is a single request-header condition.
//
// A condition is satisfied iff the request carries the header with at
// least one value in Values (required only when Values is non-empty)
// and no value in NotValues. A missing header fails a condition that
// requires Values and satisfies one that only has NotValues.
type Condition struct {
	Key       string
	Values    []string
	NotValues []string
}

// Rule is a conjunction of its three parts; an absent part imposes no
// constraint. A fully empty rule matches every request.
type Rule struct {
	From      *Source
	Operation *Operation
	When      []Condition
}

// Policy is a named workload authorization policy.
type Policy struct {
	Name      string
	Namespace string
	Selector  map[string]string
	Action    Action
	Rules     []Rule
}

// PolicyRef identifies a policy in evaluation evidence.
type PolicyRef struct {
	Name      string
	Namespace string
	Action    Action
}

// Evidence explains how a decision was reached, for auditing.
type Evidence struct {
	// Version is the policy-set version the evaluation ran against.
	Version uint64
	// DecisionPolicies are the matched policies that produced the
	// decision. Empty when the decision is the default allow (no
	// applicable allow policies) or a deny caused by allow policies
	// that all missed.
	DecisionPolicies []PolicyRef
	// AuditPolicies are all applicable AUDIT policies that matched,
	// sorted by name then namespace. They never affect the decision.
	AuditPolicies []PolicyRef
}

// Result is the outcome of evaluating one request.
type Result struct {
	Decision Decision
	Evidence Evidence
}
