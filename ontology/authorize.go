package ontology

import (
	"sort"
	"strings"
)

// ReasonCode explains the outcome of an access decision.
type ReasonCode string

const (
	// ReasonAllowed: every tag on the instance is allowed for the subject
	// (or the instance carries no tags).
	ReasonAllowed ReasonCode = "allowed"
	// ReasonExplicitDenyOverride: denied because an explicit deny overrode
	// an allow (error class 3).
	ReasonExplicitDenyOverride ReasonCode = "explicit-deny-override"
	// ReasonMissingGrant: denied because at least one tag has no allow
	// conclusion (default-deny, no explicit deny involved).
	ReasonMissingGrant ReasonCode = "missing-grant"
)

// TagDecision is the per-tag basis of a decision.
type TagDecision struct {
	Tag     string       `json:"tag"`
	Allowed bool         `json:"allowed"`
	Basis   []GrantBasis `json:"basis"`
}

// Decision is the outcome of an access check. Tags and the basis entries
// inside each tag are sorted by ID, so a decision never depends on the
// order in which tags were recorded on the instance or grants were
// registered in the grant table.
type Decision struct {
	Allowed bool           `json:"allowed"`
	Reason  ReasonCode     `json:"reason"`
	Tags    []TagDecision  `json:"tags"`
	Stats   TraversalStats `json:"stats"`
}

// tagConclusion is the subject-level conclusion for one tag.
type tagConclusion struct {
	hasAllow    bool
	hasDeny     bool
	basis       []GrantBasis
	cyclicRoles []string
}

// concludeLocked computes the subject's conclusion for one tag.
//
// Subject-level combination rule: an explicit deny from any of the
// subject's roles overrides every allow (deny-override). Roles whose
// hierarchy contains a cycle contribute nothing; they are reported
// separately so the caller can apply the error-class priority.
//
// Caller must hold the read lock.
func (e *Engine) concludeLocked(subject, tag string) tagConclusion {
	var c tagConclusion
	roles := make([]string, 0, len(e.subjectRoles[subject]))
	for r := range e.subjectRoles[subject] {
		roles = append(roles, r)
	}
	sort.Strings(roles)
	for _, r := range roles {
		if e.roleCyclic[r] {
			c.cyclicRoles = append(c.cyclicRoles, r)
			continue
		}
		info, ok := e.roleGrants[r][tag]
		if !ok {
			continue
		}
		if info.Effect == Deny {
			c.hasDeny = true
		} else {
			c.hasAllow = true
		}
		c.basis = append(c.basis, GrantBasis{
			Role:     r,
			Tag:      tag,
			Effect:   info.Effect,
			Direct:   info.Direct(),
			Distance: info.Distance,
		})
	}
	return c
}

// finalizeLocked applies the error-class priority to a concluded decision.
//
// Fixed priority: explicit-deny-override (class 3) is reported before
// role-hierarchy-cycle (class 4). A cyclic role makes the grant source
// undeterminable, so unless a higher-priority class already decided the
// outcome, the check fails with the cycle error.
func finalize(dec Decision, cyclicRoles []string, subject string) (Decision, error) {
	if !dec.Allowed && dec.Reason == ReasonExplicitDenyOverride {
		return dec, nil
	}
	if len(cyclicRoles) > 0 {
		sort.Strings(cyclicRoles)
		return dec, roleCycleErr(subject, "role hierarchy cycle via "+strings.Join(cyclicRoles, ","))
	}
	return dec, nil
}

// Authorize decides whether subject may perform action on instance.
//
// Final combination rule over the instance's tags: the access is allowed
// only when every carried tag concludes allow; a single explicit deny
// produces ReasonExplicitDenyOverride, otherwise any tag without an allow
// conclusion produces ReasonMissingGrant (default-deny). The rule is a set
// aggregation and therefore independent of tag and grant ordering.
//
// The check is pure: it takes the read lock only and never mutates tag
// inheritance state, the role hierarchy, or any clock.
func (e *Engine) Authorize(subject, instance, action string) (Decision, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	input := map[string]string{"subject": subject, "instance": instance, "action": action}
	dec, err := e.authorizeLocked(subject, instance)
	e.audit.Log(AuditEvent{Op: "Authorize", Input: input, Output: dec, Err: errString(err)})
	return dec, err
}

func (e *Engine) authorizeLocked(subject, instance string) (Decision, error) {
	// Class 1: existence.
	if _, ok := e.subjectRoles[subject]; !ok {
		return Decision{}, notFoundErr(subject, "subject not declared")
	}
	objectType, ok := e.instances[instance]
	if !ok {
		return Decision{}, notFoundErr(subject, "instance not declared: "+instance)
	}

	tags := make([]string, 0, len(e.typeTags[objectType]))
	for tag := range e.typeTags[objectType] {
		if _, ok := e.tags[tag]; !ok {
			return Decision{}, notFoundErr(subject, "tag not declared: "+tag)
		}
		tags = append(tags, tag)
	}
	sort.Strings(tags)

	dec := Decision{Allowed: true, Reason: ReasonAllowed}
	cyclicSet := map[string]struct{}{}
	explicitDeny := false
	missing := false
	for _, tag := range tags {
		c := e.concludeLocked(subject, tag)
		for _, r := range c.cyclicRoles {
			cyclicSet[r] = struct{}{}
		}
		td := TagDecision{Tag: tag, Basis: c.basis}
		switch {
		case c.hasDeny:
			explicitDeny = true
		case c.hasAllow:
			td.Allowed = true
		default:
			missing = true
		}
		dec.Tags = append(dec.Tags, td)
	}
	dec.Stats = TraversalStats{
		TagSourcesEvaluated: len(tags),
		RoleNodesVisited:    len(e.subjectRoles[subject]),
	}
	switch {
	case explicitDeny:
		dec.Allowed = false
		dec.Reason = ReasonExplicitDenyOverride
	case missing:
		dec.Allowed = false
		dec.Reason = ReasonMissingGrant
	}
	var cyclic []string
	for r := range cyclicSet {
		cyclic = append(cyclic, r)
	}
	return finalize(dec, cyclic, subject)
}

// CheckTag decides whether subject may access instance with respect to one
// specific tag. It additionally reports error class 2: the tag would reach
// the instance's object type but every inheritance path is blocked.
func (e *Engine) CheckTag(subject, instance, tag string) (Decision, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	input := map[string]string{"subject": subject, "instance": instance, "tag": tag}
	dec, err := e.checkTagLocked(subject, instance, tag)
	e.audit.Log(AuditEvent{Op: "CheckTag", Input: input, Output: dec, Err: errString(err)})
	return dec, err
}

func (e *Engine) checkTagLocked(subject, instance, tag string) (Decision, error) {
	// Class 1: existence.
	if _, ok := e.subjectRoles[subject]; !ok {
		return Decision{}, notFoundErr(subject, "subject not declared")
	}
	if _, ok := e.tags[tag]; !ok {
		return Decision{}, notFoundErr(subject, "tag not declared: "+tag)
	}
	objectType, ok := e.instances[instance]
	if !ok {
		return Decision{}, notFoundErr(subject, "instance not declared: "+instance)
	}

	// Class 2: tag absent because every inheritance path is blocked.
	if _, carried := e.typeTags[objectType][tag]; !carried {
		if _, blocked := e.typeBlocked[objectType][tag]; blocked {
			return Decision{}, blockedErr(subject, tag,
				"every inheritance path of the tag passes a blocking point")
		}
		// The tag is simply not present on the instance: no restriction.
		return Decision{Allowed: true, Reason: ReasonAllowed}, nil
	}

	c := e.concludeLocked(subject, tag)
	td := TagDecision{Tag: tag, Basis: c.basis}
	dec := Decision{
		Allowed: true,
		Reason:  ReasonAllowed,
		Tags:    []TagDecision{td},
		Stats: TraversalStats{
			TagSourcesEvaluated: 1,
			RoleNodesVisited:    len(e.subjectRoles[subject]),
		},
	}
	switch {
	case c.hasDeny:
		dec.Allowed = false
		dec.Reason = ReasonExplicitDenyOverride
	case !c.hasAllow:
		dec.Allowed = false
		dec.Reason = ReasonMissingGrant
	default:
		dec.Tags[0].Allowed = true
	}
	return finalize(dec, c.cyclicRoles, subject)
}

// InstanceTags returns the effective tags of an instance, sorted by ID.
func (e *Engine) InstanceTags(instance string) ([]string, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	objectType, ok := e.instances[instance]
	if !ok {
		return nil, ErrInstanceUnknown
	}
	tags := make([]string, 0, len(e.typeTags[objectType]))
	for tag := range e.typeTags[objectType] {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return tags, nil
}
