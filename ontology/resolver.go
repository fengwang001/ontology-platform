package ontology

import "sort"

// Verdict is the final traversal decision for one candidate link.
type Verdict uint8

const (
	// VerdictInvalid means the subject identifier is illegal.
	VerdictInvalid Verdict = iota
	// VerdictAllow permits traversal.
	VerdictAllow
	// VerdictDeny forbids traversal.
	VerdictDeny
	// VerdictAmbiguous means the two orthogonal precedence axes conflict at
	// the same priority and no single overlay result exists.
	VerdictAmbiguous
)

func (v Verdict) String() string {
	switch v {
	case VerdictAllow:
		return "allow"
	case VerdictDeny:
		return "deny"
	case VerdictAmbiguous:
		return "ambiguous"
	default:
		return "invalid"
	}
}

// layerVote is the merged vote of the highest-priority groups within one layer.
type layerVote struct {
	declared bool      // any group in the winning tier declared here
	decision Decision  // merged decision (deny-wins) within the winning tier
	groups   []GroupID // groups in the winning tier that declared
	priority int       // priority number of the winning tier
	conflict bool      // the winning tier contained both Allow and Deny
}

// VerdictResult explains the overlay decision for one candidate link.
type VerdictResult struct {
	Verdict Verdict

	Subject  SubjectID
	LinkType LinkType
	FromType ObjectType
	ToType   ObjectType

	Link           layerVote
	FromObjectVote layerVote
	ToObjectVote   layerVote
	ObjectMerged   Decision // deny-wins merge of the two endpoint votes

	// Reason is a human-readable, machine-stable description of the basis.
	Reason string
}

// resolver evaluates the overlay rules against a pinned snapshot.
type resolver struct {
	snap *snapshot
}

func newResolver(snap *snapshot) *resolver {
	return &resolver{snap: snap}
}

// DecideAt resolves one candidate link against an explicit pinned snapshot.
// It is the public entry point for point-in-time boundary checks.
func DecideAt(snap *snapshot, subject SubjectID, lt LinkType, fromType, toType ObjectType) VerdictResult {
	return newResolver(snap).resolve(subject, lt, fromType, toType)
}

// winningTier computes the merged vote of the highest-priority groups (among
// the subject's memberships) that declare on key. It only iterates groups the
// subject actually belongs to, never all groups.
func (r *resolver) winningTier(
	subject SubjectID,
	decls map[GroupID]map[string]Decision,
	key string,
) layerVote {
	vote := layerVote{priority: minInt}
	memberGroups, ok := r.snap.membership[subject]
	if !ok {
		return vote
	}
	bestPriority := minInt
	for g := range memberGroups {
		group, exists := r.snap.groups[g]
		if !exists {
			continue
		}
		byKey, ok := decls[g]
		if !ok {
			continue
		}
		d, ok := byKey[key]
		if !ok {
			continue
		}
		switch {
		case group.Priority > bestPriority:
			bestPriority = group.Priority
			vote = layerVote{priority: group.Priority, decision: d, declared: true}
			vote.groups = []GroupID{g}
		case group.Priority == bestPriority:
			vote.groups = append(vote.groups, g)
			if d != vote.decision {
				vote.conflict = true
				vote.decision = Deny // deny wins ties inside one layer
			}
		}
	}
	sort.Slice(vote.groups, func(i, j int) bool { return vote.groups[i] < vote.groups[j] })
	return vote
}

const minInt = -int(^uint(0)>>1) - 1

func objectDeclsView(snap *snapshot) map[GroupID]map[string]Decision {
	m := make(map[GroupID]map[string]Decision, len(snap.objectDecls))
	for g, d := range snap.objectDecls {
		cp := make(map[string]Decision, len(d))
		for t, v := range d {
			cp[string(t)] = v
		}
		m[g] = cp
	}
	return m
}

func linkDeclsView(snap *snapshot) map[GroupID]map[string]Decision {
	m := make(map[GroupID]map[string]Decision, len(snap.linkDecls))
	for g, d := range snap.linkDecls {
		cp := make(map[string]Decision, len(d))
		for t, v := range d {
			cp[string(t)] = v
		}
		m[g] = cp
	}
	return m
}

// resolve applies the full overlay precedence for a single candidate link.
func (r *resolver) resolve(subject SubjectID, lt LinkType, fromType, toType ObjectType) VerdictResult {
	res := VerdictResult{
		Subject:  subject,
		LinkType: lt,
		FromType: fromType,
		ToType:   toType,
	}

	// 1. Illegal subject outranks every declaration.
	if !ValidSubject(subject) {
		res.Verdict = VerdictInvalid
		res.Reason = "invalid-subject"
		return res
	}

	linkDecls := linkDeclsView(r.snap)
	objectDecls := objectDeclsView(r.snap)

	// Both layers are evaluated against the pinned snapshot before the
	// precedence rules are applied.
	res.Link = r.winningTier(subject, linkDecls, string(lt))
	res.FromObjectVote = r.winningTier(subject, objectDecls, string(fromType))
	res.ToObjectVote = r.winningTier(subject, objectDecls, string(toType))

	// 2. Link-type layer: an explicit Allow/Deny overrides the default.
	if res.Link.declared {
		// Cross-axis ambiguity takes precedence over the layer override:
		// equally high-priority groups contradict each other across the two
		// layers, and the two natural merge orders disagree.
		if res.Link.decision == Allow && crossAxisDenyGroups(res.Link, res.FromObjectVote, res.ToObjectVote) {
			res.ObjectMerged = objectFallback(res.FromObjectVote, res.ToObjectVote)
			res.Verdict = VerdictAmbiguous
			res.Reason = "ambiguous:equal-priority link-allow vs object-deny"
			return res
		}
		if res.Link.decision == Allow {
			res.Verdict = VerdictAllow
			res.Reason = "link-layer-allow"
		} else {
			res.Verdict = VerdictDeny
			res.Reason = "link-layer-deny"
		}
		if res.Link.conflict {
			res.Reason += ";tier-deny-wins"
		}
		return res
	}

	// 3. Object-type layer: merge the two endpoint votes with deny-wins.
	switch {
	case res.FromObjectVote.declared && res.ToObjectVote.declared:
		res.ObjectMerged = mergeDenyWins(res.FromObjectVote.decision, res.ToObjectVote.decision)
	case res.FromObjectVote.declared:
		res.ObjectMerged = res.FromObjectVote.decision
	case res.ToObjectVote.declared:
		res.ObjectMerged = res.ToObjectVote.decision
	default:
		// 4. Neither layer declares anything: default deny.
		res.Verdict = VerdictDeny
		res.Reason = "default-deny"
		return res
	}

	if res.ObjectMerged == Allow {
		res.Verdict = VerdictAllow
		res.Reason = "object-layer-allow"
	} else {
		res.Verdict = VerdictDeny
		res.Reason = "object-layer-deny"
	}
	if res.FromObjectVote.conflict || res.ToObjectVote.conflict {
		res.Reason += ";tier-deny-wins"
	}
	return res
}

func mergeDenyWins(a, b Decision) Decision {
	if a == Deny || b == Deny {
		return Deny
	}
	return Allow
}

func objectFallback(from, to layerVote) Decision {
	switch {
	case from.declared && to.declared:
		return mergeDenyWins(from.decision, to.decision)
	case from.declared:
		return from.decision
	case to.declared:
		return to.decision
	default:
		return Unset
	}
}

// crossAxisDenyGroups reports whether a group distinct from the link-tier
// groups produces an object-type Deny at the same priority as the link tier.
// Link-Deny never clashes (both merge orders deny); different priorities are
// settled by the stated layer order; same-group declarations are not
// "multiple groups contradicting" and follow the link override.
func crossAxisDenyGroups(link, from, to layerVote) bool {
	objectMerged := objectFallback(from, to)
	if objectMerged != Deny {
		return false
	}
	linkGroups := map[GroupID]bool{}
	for _, g := range link.groups {
		linkGroups[g] = true
	}
	denying := func(vote layerVote) bool {
		if !vote.declared || vote.decision != Deny || vote.priority != link.priority {
			return false
		}
		for _, g := range vote.groups {
			if !linkGroups[g] {
				return true
			}
		}
		return false
	}
	return denying(from) || denying(to)
}
