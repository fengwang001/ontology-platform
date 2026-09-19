package projection

// DependencyPolicy selects what happens when a computed field's declared
// source is hidden but the computed field itself would be visible.
type DependencyPolicy int

const (
	// DependencyHideResult hides the computed field along with its source.
	DependencyHideResult DependencyPolicy = iota
	// DependencyError rejects the whole projection with an explanatory error.
	DependencyError
)

// FieldSpec declares structural constraints for one attribute. Paths use the
// same dotted syntax as rules (e.g. "addr.geo").
type FieldSpec struct {
	// Required means a projection that hides this attribute must fail
	// instead of silently returning an object missing the attribute.
	Required bool
	// ComputedFrom names the attribute this field is computed from. When
	// non-empty and the source is hidden, DependencyPolicy is applied.
	ComputedFrom string
}

// Schema declares an object type's structural constraints. A nil or empty
// Schema performs projection without required-field or dependency checks.
type Schema struct {
	Fields           map[string]FieldSpec
	DependencyPolicy DependencyPolicy
}

// Effect is the outcome a rule has on a field.
type Effect int

const (
	EffectAllow Effect = iota
	EffectDeny
)

func (e Effect) String() string {
	if e == EffectAllow {
		return "allow"
	}
	return "deny"
}

// Reason explains why a field is visible or hidden.
type Reason int

const (
	// ReasonDefault: no rule matched; the rule set's default effect applies.
	ReasonDefault Reason = iota
	// ReasonDirectRule: a rule explicitly matching this field decided it.
	ReasonDirectRule
	// ReasonAncestorOverride: an exact deny rule on an ancestor hides this
	// descendant regardless of its own rules.
	ReasonAncestorOverride
)

// Decision is the answer to an Explain query.
type Decision struct {
	Path    string
	Visible bool
	Effect  Effect
	Reason  Reason
	// RuleRaw is the original rule text that governs the field. It is empty
	// when Reason is ReasonDefault.
	RuleRaw string
	// OverriddenBy is the ancestor deny rule's original text when
	// Reason is ReasonAncestorOverride; otherwise empty.
	OverriddenBy string
}
