package ontology

// Effect is an explicit authorization effect. Deny always wins over Allow for
// the same subject/object/action triple.
type Effect int

const (
	Deny Effect = iota
	Allow
)

// OverrideMode is the override declared by a target object type.
type OverrideMode int

const (
	// NoOverride: upstream propagated results are accepted normally.
	NoOverride OverrideMode = iota
	// ReplaceOverride: at this target the propagated union is replaced by the
	// target's own direct authorizations; propagation may continue downstream
	// starting from those direct authorizations.
	ReplaceOverride
	// BlockOverride: like ReplaceOverride, and additionally propagation is
	// severed here: nothing continues further downstream even if depth remains.
	BlockOverride
)

// LinkType is a directed edge in the object-type graph. PropagationDepth 0
// means the link participates in direct authorization only and never carries
// propagated permissions. A positive depth d means a permission can cross
// this link while d > 0 and its remaining budget after crossing is
// min(previousRemaining, d) - 1.
type LinkType struct {
	Name             string
	From             string
	To               string
	PropagationDepth int
}

// propagates reports whether the link takes part in propagation at all.
func (l LinkType) propagates() bool { return l.PropagationDepth > 0 }

// Grant is an explicit authorization record for a subject on an object type.
type Grant struct {
	Subject string
	Object  string
	Action  string
	Effect  Effect
}

// DenialReason distinguishes the non-erroneous reasons a propagated
// permission may be absent at a target.
type DenialReason string

const (
	// DenialExplicit: an explicit deny record outranks the propagated allow.
	DenialExplicit DenialReason = "explicit_deny"
	// DenialBlocked: a BlockOverride severed propagation before the target.
	DenialBlocked DenialReason = "override_blocked"
	// DenialReplaced: a ReplaceOverride replaced the upstream union without a
	// matching direct authorization of its own.
	DenialReplaced DenialReason = "override_replaced"
	// DenialDepth: the only structural paths ran out of propagation depth.
	DenialDepth DenialReason = "depth_exhausted"
	// DenialNone: no grant exists for the subject/action and no path reaches
	// the target from a grant origin.
	DenialNone DenialReason = "no_grant"
)

// Decision is the observable result of one permission query.
type Decision struct {
	Subject string
	Object  string
	Action  string
	// Allowed is the final answer.
	Allowed bool
	// Basis explains which origins and link hops produced the answer.
	Basis []string
	// DenyReason is set when Allowed is false; it distinguishes explicit
	// deny, override blocking, natural depth exhaustion and plain absence.
	DenyReason DenialReason
}
