// Package abac implements attribute-based access control (ABAC).
//
// A decision is computed from subject attributes, resource attributes,
// environment context and a set of policies. Policies combine with
// deny-overrides semantics, attributes referenced by a policy that are
// missing make the policy not applicable (never treated as a false
// value), and decisions never reveal whether a resource exists.
package abac

// Attributes is a bag of named attribute values for a subject,
// resource or the environment.
type Attributes map[string]any

// Scope identifies which attribute bag a condition reads from.
type Scope int

const (
	ScopeSubject Scope = iota
	ScopeResource
	ScopeEnvironment
)

func (s Scope) String() string {
	switch s {
	case ScopeSubject:
		return "subject"
	case ScopeResource:
		return "resource"
	case ScopeEnvironment:
		return "environment"
	default:
		return "unknown"
	}
}

// Operator is the comparison applied by a condition.
type Operator int

const (
	OpEq Operator = iota
	OpNeq
	OpGt
	OpGte
	OpLt
	OpLte
	OpIn
)

func (op Operator) String() string {
	switch op {
	case OpEq:
		return "eq"
	case OpNeq:
		return "neq"
	case OpGt:
		return "gt"
	case OpGte:
		return "gte"
	case OpLt:
		return "lt"
	case OpLte:
		return "lte"
	case OpIn:
		return "in"
	default:
		return "unknown"
	}
}

// Condition compares one attribute from a scope against a value.
// If the referenced attribute is missing, the condition is not
// satisfied and the enclosing policy is not applicable.
type Condition struct {
	Scope Scope
	Key   string
	Op    Operator
	Value any
}

// Effect is the outcome a policy contributes when it matches.
type Effect int

const (
	EffectPermit Effect = iota
	EffectDeny
)

func (e Effect) String() string {
	if e == EffectDeny {
		return "deny"
	}
	return "permit"
}

// Policy is a set of ANDed conditions with an effect.
type Policy struct {
	ID         string
	Effect     Effect
	Conditions []Condition
}

// Request is a single access evaluation request.
type Request struct {
	Subject     Attributes
	Resource    Attributes // nil means the resource does not exist
	Environment Attributes
	Action      string
}

// ReasonCode distinguishes why a decision was reached.
type ReasonCode int

const (
	// ReasonPermitted: at least one permit policy matched and no deny matched.
	ReasonPermitted ReasonCode = iota
	// ReasonDeniedByPolicy: a deny policy matched (deny overrides permit).
	ReasonDeniedByPolicy
	// ReasonNoApplicablePermit: no permit policy matched. Also returned
	// when the resource does not exist, so existence is never leaked.
	ReasonNoApplicablePermit
)

func (r ReasonCode) String() string {
	switch r {
	case ReasonPermitted:
		return "permitted"
	case ReasonDeniedByPolicy:
		return "denied-by-policy"
	case ReasonNoApplicablePermit:
		return "no-applicable-permit"
	default:
		return "unknown"
	}
}

// Decision is the result of evaluating a request.
type Decision struct {
	Allowed bool
	Reason  ReasonCode
	// MatchedPolicies lists IDs of policies whose conditions all matched,
	// sorted by policy ID, forming the decision basis.
	MatchedPolicies []string
}
