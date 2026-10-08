// Package authz implements a service-mesh workload authorization policy
// evaluator: scoped applicability, deny-first ordering, default-allow when
// no allow policy applies, empty-rule/negation semantics, and atomic
// versioned publication of the whole policy set.
package authz

// Action is the effect of a policy when one of its rules matches.
type Action int

const (
	ActionAllow Action = iota
	ActionDeny
	ActionAudit
)

func (a Action) String() string {
	switch a {
	case ActionAllow:
		return "ALLOW"
	case ActionDeny:
		return "DENY"
	case ActionAudit:
		return "AUDIT"
	}
	return "UNKNOWN"
}

// ActionValid reports whether a is one of the three legal actions.
func ActionValid(a Action) bool {
	return a == ActionAllow || a == ActionDeny || a == ActionAudit
}

// Decision is the final verdict of an evaluation.
type Decision int

const (
	DecisionAllow Decision = iota
	DecisionDeny
)

func (d Decision) String() string {
	if d == DecisionDeny {
		return "DENY"
	}
	return "ALLOW"
}

// Request describes a single workload-to-workload call.
type Request struct {
	SourceIdentity  string
	SourceNamespace string
	TargetNamespace string
	TargetLabels    map[string]string
	Method          string
	Path            string
	Port            int
	// Headers maps header names (case-insensitive) to their values; a
	// name may carry multiple values.
	Headers map[string][]string
}

// PolicyRef identifies a policy in evaluation evidence.
type PolicyRef struct {
	Name      string
	Namespace string
	Action    Action
}

// Result is the outcome of evaluating one request, with audit evidence.
type Result struct {
	Decision Decision
	// Version is the policy-set version this evaluation observed.
	Version uint64
	// DecisionPolicies lists the matched policies that produced the
	// decision. It is empty when the request is allowed because no allow
	// policy applies, and when the request is denied because no allow
	// policy matched. Sorted by (Name, Namespace).
	DecisionPolicies []PolicyRef
	// AuditPolicies lists every applicable audit policy that matched the
	// request. It never influences Decision. Sorted by (Name, Namespace).
	AuditPolicies []PolicyRef
}

// SourceSpec constrains the caller. A nil *SourceSpec imposes no constraint.
type SourceSpec struct {
	Identities    []string
	NotIdentities []string
	Namespaces    []string
	NotNamespaces []string
}

// OperationSpec constrains the operation. A nil *OperationSpec imposes no
// constraint.
type OperationSpec struct {
	Methods    []string
	NotMethods []string
	Paths      []string
	NotPaths   []string
	Ports      []int
	NotPorts   []int
}

// Condition constrains one request header (name case-insensitive).
type Condition struct {
	Header    string
	Values    []string
	NotValues []string
}

// Rule is a conjunction of three optional parts; an absent part (nil /
// empty) imposes no constraint. A fully empty rule matches every request.
type Rule struct {
	Source    *SourceSpec
	Operation *OperationSpec
	// Conditions is the condition part; empty means the part is absent.
	Conditions []Condition
}

// Policy is a named workload authorization policy.
type Policy struct {
	Name      string
	Namespace string
	// Selector must be fully satisfied by the target workload labels;
	// empty selects every workload in the policy's scope.
	Selector map[string]string
	Action   Action
	// Rules: the policy matches iff at least one rule matches. An empty
	// rule list matches nothing.
	Rules []Rule
}
