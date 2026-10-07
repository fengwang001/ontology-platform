package ontology

// naive is an independent reference implementation used only by tests. It
// deliberately uses different algorithms than the engine: per-query DFS
// reachability instead of materialized BFS fixpoints, and simple-path
// enumeration for role distances instead of level-order aggregation. The
// differential test feeds identical declaration sequences to both and
// compares every decision.
type naive struct {
	objectTypes  map[string]bool
	links        map[string]LinkType
	tags         map[string]bool
	attach       map[string]map[string]bool
	props        map[propKey]map[Direction]bool
	blocks       map[blockKey]bool
	roleParents  map[string]map[string]bool
	subjectRoles map[string]map[string]bool
	grants       map[grantKey]Effect
	instances    map[string]string
}

func newNaive() *naive {
	return &naive{
		objectTypes:  map[string]bool{},
		links:        map[string]LinkType{},
		tags:         map[string]bool{},
		attach:       map[string]map[string]bool{},
		props:        map[propKey]map[Direction]bool{},
		blocks:       map[blockKey]bool{},
		roleParents:  map[string]map[string]bool{},
		subjectRoles: map[string]map[string]bool{},
		grants:       map[grantKey]Effect{},
		instances:    map[string]string{},
	}
}

// reaches reports whether tag can travel from object type `from` to object
// type `target` along propagation edges, honoring (or ignoring) blocks.
func (n *naive) reaches(tag, from, target string, ignoreBlocks bool) bool {
	visited := map[string]bool{from: true}
	var dfs func(cur string) bool
	dfs = func(cur string) bool {
		if cur == target {
			return true
		}
		for k, dirs := range n.props {
			if k.tag != tag {
				continue
			}
			if !ignoreBlocks && n.blocks[blockKey{tag: tag, link: k.link}] {
				continue
			}
			link, ok := n.links[k.link]
			if !ok {
				continue
			}
			for _, d := range []Direction{Downstream, Upstream} {
				if !dirs[d] {
					continue
				}
				var next string
				if d == Downstream {
					if link.From != cur {
						continue
					}
					next = link.To
				} else {
					if link.To != cur {
						continue
					}
					next = link.From
				}
				if !visited[next] {
					visited[next] = true
					if dfs(next) {
						return true
					}
				}
			}
		}
		return false
	}
	return dfs(from)
}

func (n *naive) tagsOf(objectType string) map[string]bool {
	out := map[string]bool{}
	for tag := range n.tags {
		for src, set := range n.attach {
			if !set[tag] {
				continue
			}
			if n.reaches(tag, src, objectType, false) {
				out[tag] = true
				break
			}
		}
	}
	return out
}

func (n *naive) blockedAt(objectType string) map[string]bool {
	out := map[string]bool{}
	for tag := range n.tags {
		actual := false
		potential := false
		for src, set := range n.attach {
			if !set[tag] {
				continue
			}
			if n.reaches(tag, src, objectType, false) {
				actual = true
			}
			if n.reaches(tag, src, objectType, true) {
				potential = true
			}
		}
		if potential && !actual {
			out[tag] = true
		}
	}
	return out
}

// roleCyclic reports whether the ancestor closure of role contains a cycle,
// detected as: some ancestor can reach itself through at least one edge.
func (n *naive) roleCyclic(role string) bool {
	ancestors := map[string]bool{}
	var collect func(r string)
	collect = func(r string) {
		for p := range n.roleParents[r] {
			if !ancestors[p] {
				ancestors[p] = true
				collect(p)
			}
		}
	}
	ancestors[role] = true
	collect(role)
	for a := range ancestors {
		// Can a reach itself via at least one parent edge?
		visited := map[string]bool{}
		var seek func(r string) bool
		seek = func(r string) bool {
			for p := range n.roleParents[r] {
				if p == a {
					return true
				}
				if !visited[p] {
					visited[p] = true
					if seek(p) {
						return true
					}
				}
			}
			return false
		}
		if seek(a) {
			return true
		}
	}
	return false
}

// effectiveGrant resolves (role, tag) by enumerating all simple paths to
// ancestors, keeping the minimum distance per ancestor, then applying
// nearest-distance-wins with deny breaking ties.
func (n *naive) effectiveGrant(role, tag string) (Effect, bool) {
	best := map[string]int{role: 0}
	onPath := map[string]bool{role: true}
	var dfs func(cur string, dist int)
	dfs = func(cur string, dist int) {
		for p := range n.roleParents[cur] {
			if onPath[p] {
				continue
			}
			if d, ok := best[p]; !ok || dist+1 < d {
				best[p] = dist + 1
			}
			onPath[p] = true
			dfs(p, dist+1)
			delete(onPath, p)
		}
	}
	dfs(role, 0)

	bestDist := -1
	denyAtBest := false
	for k, effect := range n.grants {
		if k.tag != tag {
			continue
		}
		d, ok := best[k.role]
		if !ok {
			continue
		}
		if bestDist == -1 || d < bestDist {
			bestDist = d
			denyAtBest = effect == Deny
		} else if d == bestDist && effect == Deny {
			denyAtBest = true
		}
	}
	if bestDist == -1 {
		return Allow, false
	}
	if denyAtBest {
		return Deny, true
	}
	return Allow, true
}

type naiveOutcome struct {
	allowed bool
	reason  ReasonCode
	class   ErrorClass // zero when no classified error
}

// conclude computes the subject-level conclusion for one tag.
func (n *naive) conclude(subject, tag string) (hasAllow, hasDeny bool, cyclic []string) {
	for role := range n.subjectRoles[subject] {
		if n.roleCyclic(role) {
			cyclic = append(cyclic, role)
			continue
		}
		effect, ok := n.effectiveGrant(role, tag)
		if !ok {
			continue
		}
		if effect == Deny {
			hasDeny = true
		} else {
			hasAllow = true
		}
	}
	return hasAllow, hasDeny, cyclic
}

func (n *naive) authorize(subject, instance string) naiveOutcome {
	if _, ok := n.subjectRoles[subject]; !ok {
		return naiveOutcome{class: ClassNotFound}
	}
	objectType, ok := n.instances[instance]
	if !ok {
		return naiveOutcome{class: ClassNotFound}
	}
	explicitDeny := false
	missing := false
	cyclicSeen := map[string]bool{}
	var cyclic []string
	for tag := range n.tagsOf(objectType) {
		hasAllow, hasDeny, cyc := n.conclude(subject, tag)
		for _, r := range cyc {
			if !cyclicSeen[r] {
				cyclicSeen[r] = true
				cyclic = append(cyclic, r)
			}
		}
		if hasDeny {
			explicitDeny = true
		} else if !hasAllow {
			missing = true
		}
	}
	if explicitDeny {
		return naiveOutcome{allowed: false, reason: ReasonExplicitDenyOverride}
	}
	if len(cyclic) > 0 {
		return naiveOutcome{class: ClassRoleCycle}
	}
	if missing {
		return naiveOutcome{allowed: false, reason: ReasonMissingGrant}
	}
	return naiveOutcome{allowed: true, reason: ReasonAllowed}
}

func (n *naive) checkTag(subject, instance, tag string) naiveOutcome {
	if _, ok := n.subjectRoles[subject]; !ok {
		return naiveOutcome{class: ClassNotFound}
	}
	if !n.tags[tag] {
		return naiveOutcome{class: ClassNotFound}
	}
	objectType, ok := n.instances[instance]
	if !ok {
		return naiveOutcome{class: ClassNotFound}
	}
	if !n.tagsOf(objectType)[tag] {
		if n.blockedAt(objectType)[tag] {
			return naiveOutcome{class: ClassAllPathsBlocked}
		}
		return naiveOutcome{allowed: true, reason: ReasonAllowed}
	}
	hasAllow, hasDeny, cyclic := n.conclude(subject, tag)
	if hasDeny {
		return naiveOutcome{allowed: false, reason: ReasonExplicitDenyOverride}
	}
	if len(cyclic) > 0 {
		return naiveOutcome{class: ClassRoleCycle}
	}
	if !hasAllow {
		return naiveOutcome{allowed: false, reason: ReasonMissingGrant}
	}
	return naiveOutcome{allowed: true, reason: ReasonAllowed}
}
