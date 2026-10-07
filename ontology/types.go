// Package ontology implements a role-and-tag based access control module.
//
// Tags are granted to the relation network formed by object types and link
// types; instances inherit tags through their membership in that network.
// Role-to-tag allow/deny grants (with role-hierarchy inheritance) decide
// whether a subject may finally operate on an instance.
package ontology

// Direction is the direction a tag propagates along a link type.
type Direction int

const (
	// Downstream propagates from the From object type to the To object type.
	Downstream Direction = iota
	// Upstream propagates from the To object type back to the From object type.
	Upstream
)

func (d Direction) String() string {
	if d == Downstream {
		return "downstream"
	}
	return "upstream"
}

// Effect is the effect of a single grant declaration to a tag.
type Effect int

const (
	// Allow explicitly allows.
	Allow Effect = iota
	// Deny explicitly denies.
	Deny
)

func (e Effect) String() string {
	if e == Deny {
		return "deny"
	}
	return "allow"
}

// LinkType connects two object types and forms one edge of the relation
// network. The direction from From to To is called downstream.
type LinkType struct {
	ID   string
	From string // source object type ID
	To   string // target object type ID
}

// propKey identifies a propagation declaration: tag travels along link.
type propKey struct {
	tag  string
	link string
}

// blockKey identifies a propagation blocking point: tag must not cross link.
type blockKey struct {
	tag  string
	link string
}

// grantKey identifies a direct role grant declaration.
type grantKey struct {
	role string
	tag  string
}

// GrantInfo is the materialized effective grant of a role for one tag.
type GrantInfo struct {
	Effect   Effect // effective conclusion
	Distance int    // inheritance distance of the source, 0 means direct
}

// Direct reports whether the conclusion comes from a direct declaration.
func (g GrantInfo) Direct() bool { return g.Distance == 0 }

// TagSource describes how one tag arrived on an instance, used for audit
// and decision explanation.
type TagSource struct {
	Tag        string `json:"tag"`
	ObjectType string `json:"objectType"` // object type on which the tag is effective
	Direct     bool   `json:"direct"`     // directly attached to that type
}

// GrantBasis describes the role evidence behind a subject's conclusion for
// one tag, used for audit and decision explanation.
type GrantBasis struct {
	Role     string `json:"role"`
	Tag      string `json:"tag"`
	Effect   Effect `json:"effect"`
	Direct   bool   `json:"direct"`   // direct declaration vs inherited from a parent role
	Distance int    `json:"distance"` // inheritance distance, 0 means direct
}

// TraversalStats records the actual work performed by one access check.
// It is the observable proof that check cost depends only on the tags and
// roles relevant to the instance, not on the total network size.
type TraversalStats struct {
	// TagSourcesEvaluated is the number of tag sources evaluated (the tags
	// actually carried by the instance).
	TagSourcesEvaluated int `json:"tagSourcesEvaluated"`
	// RoleNodesVisited is the number of role nodes visited (the subject's
	// direct roles).
	RoleNodesVisited int `json:"roleNodesVisited"`
}
