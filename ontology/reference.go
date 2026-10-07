package ontology

// refAnswer is the naive model's decision for one query.
type refAnswer struct {
	allowed    bool
	denyReason DenialReason
}

// referenceModel is an intentionally simple, independent implementation used
// solely by tests. Instead of a precomputed structural index it performs a
// depth-first token walk per (subject, target, action), enumerating concrete
// propagation states. Cycles are defended against with a per-walk
// (node, remainingBudget) visited set; configuration-level cycles are rejected
// up front just like in the gateway.
type referenceModel struct {
	depthCap  int
	objects   map[string]bool
	links     map[string]LinkType
	overrides map[string]OverrideMode
	grants    map[grantKey]Effect
}

func newReferenceModel(depthCap int) *referenceModel {
	return &referenceModel{
		depthCap:  depthCap,
		objects:   map[string]bool{},
		links:     map[string]LinkType{},
		overrides: map[string]OverrideMode{},
		grants:    map[grantKey]Effect{},
	}
}

// load copies every observable configuration element from a gateway snapshot.
func (r *referenceModel) load(s *state) {
	r.objects = map[string]bool{}
	r.links = map[string]LinkType{}
	r.overrides = map[string]OverrideMode{}
	r.grants = map[grantKey]Effect{}
	for name := range s.objects {
		r.objects[name] = true
	}
	for name, link := range s.links {
		r.links[name] = link
	}
	for name, mode := range s.overrides {
		r.overrides[name] = mode
	}
	for key, effect := range s.grants {
		r.grants[key] = effect
	}
}

// refDelivered is the naive walk outcome marking successful delivery.
const refDelivered DenialReason = "__delivered__"

// decide answers one query by walking from every object type carrying a
// matching grant for the subject/action pair.
func (r *referenceModel) decide(subject, object, action string) refAnswer {
	if !r.objects[object] {
		return refAnswer{allowed: false, denyReason: DenialNone}
	}
	if effect, ok := r.grants[grantKey{subject: subject, object: object, action: action}]; ok {
		if effect == Allow {
			return refAnswer{allowed: true}
		}
		return refAnswer{allowed: false, denyReason: DenialExplicit}
	}

	gotAllow := false
	gotDeny := false
	bestReason := DenialNone
	rank := map[DenialReason]int{
		DenialNone:     0,
		DenialDepth:    1,
		DenialReplaced: 2,
		DenialBlocked:  3,
	}
	strengthen := func(reason DenialReason) {
		if rank[reason] > rank[bestReason] {
			bestReason = reason
		}
	}

	for gk, effect := range r.grants {
		if gk.subject != subject || gk.action != action {
			continue
		}
		outcomes := r.walk(gk.object, object, r.depthCap)
		for _, outcome := range outcomes {
			switch outcome {
			case refDelivered:
				if effect == Allow {
					gotAllow = true
				} else {
					gotDeny = true
				}
			default:
				strengthen(outcome)
			}
		}
	}

	if gotDeny {
		return refAnswer{allowed: false, denyReason: DenialExplicit}
	}
	if gotAllow {
		return refAnswer{allowed: true}
	}
	return refAnswer{allowed: false, denyReason: bestReason}
}

// walk returns the set of outcomes when a token starting at origin tries to
// contribute to target. Outcomes are refDelivered on success or one of the
// denial reasons if it reaches structurally only under an obstruction.
func (r *referenceModel) walk(origin, target string, remaining int) []DenialReason {
	var outcomes []DenialReason

	// Path state is carried per recursive branch so a replace/block on one
	// sibling route cannot leak onto another parallel route (matching the
	// gateway's per-(origin,target) path-independence semantics). The graph is
	// guaranteed acyclic for propagation-enabled links at commit time, so the
	// walk needs no visited set: using one would wrongly prune parallel paths
	// that reach the same node under different obstruction states.
	var visit func(node string, budget int, hopCount int, replaced, blocked, depth bool)
	visit = func(node string, budget int, hopCount int, replaced, blocked, depth bool) {
		// Override at the current node applies to incoming upstream content.
		if hopCount > 0 {
			switch r.overrides[node] {
			case BlockOverride:
				blocked = true
			case ReplaceOverride:
				replaced = true
			}
		}
		if node == target {
			switch {
			case blocked:
				outcomes = append(outcomes, DenialBlocked)
			case replaced && !blocked:
				outcomes = append(outcomes, DenialReplaced)
			case depth:
				outcomes = append(outcomes, DenialDepth)
			default:
				outcomes = append(outcomes, refDelivered)
			}
		}

		for _, link := range r.linksFrom(node) {
			if !link.propagates() {
				continue
			}
			nextBlocked := blocked
			if hopCount == 0 && r.overrides[node] == BlockOverride {
				nextBlocked = true
			}
			if !nextBlocked && !replaced {
				if budget == 0 || link.PropagationDepth == 0 {
					// Natural termination: the marker is recorded at the
					// attempted destination only and never propagates.
					if link.To == target {
						outcomes = append(outcomes, DenialDepth)
					}
					continue
				}
				newBudget := budget
				if link.PropagationDepth < newBudget {
					newBudget = link.PropagationDepth
				}
				newBudget--
				visit(link.To, newBudget, hopCount+1, false, false, false)
				continue
			}
			visit(link.To, budget, hopCount+1, replaced, nextBlocked, depth)
		}
	}

	visit(origin, remaining, 0, false, false, false)
	return outcomes
}

func (r *referenceModel) linksFrom(node string) []LinkType {
	var out []LinkType
	for _, link := range r.links {
		if link.From == node {
			out = append(out, link)
		}
	}
	return out
}
