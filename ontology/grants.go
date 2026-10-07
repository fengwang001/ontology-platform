package ontology

// recomputeGrantsLocked rebuilds roleGrants and roleCyclic from declarations.
// Caller must hold the write lock.
//
// Effective-grant rule (deterministic, order independent): for a role R and
// a tag T, collect every allow/deny declaration reachable from R through the
// parent hierarchy together with its inheritance distance (direct = 0). The
// smallest distance at which any declaration exists wins; if that distance
// contains both allow and deny, deny wins. This resolves all four
// direct/inherited x allow/deny combinations:
//
//	direct allow  vs direct deny    -> deny  (same distance, deny wins)
//	direct allow  vs inherited deny -> allow (closer distance wins)
//	inherited allow vs direct deny  -> deny  (closer distance wins)
//	inherited allow vs inherited deny -> deny at the nearest distance
//
// A role whose ancestor closure contains an inheritance cycle is marked
// cyclic: its grant source is undeterminable and decisions report the
// role-cycle error class.
func (e *Engine) recomputeGrantsLocked() {
	e.roleGrants = map[string]map[string]GrantInfo{}
	e.roleCyclic = map[string]bool{}

	for role := range e.roleParents {
		e.roleCyclic[role] = e.reachesCycle(role)
		e.roleGrants[role] = e.effectiveGrants(role)
	}
}

// reachesCycle reports whether the ancestor closure of role contains a
// cycle. Depth-first search with a path-local stack: a back edge to a node
// on the current path is a reachable cycle.
func (e *Engine) reachesCycle(role string) bool {
	const (
		white = 0 // unvisited
		gray  = 1 // on the current DFS path
		black = 2 // fully explored, no cycle below
	)
	color := map[string]int{}
	var visit func(r string) bool
	visit = func(r string) bool {
		switch color[r] {
		case gray:
			return true
		case black:
			return false
		}
		color[r] = gray
		for p := range e.roleParents[r] {
			if visit(p) {
				return true
			}
		}
		color[r] = black
		return false
	}
	return visit(role)
}

// effectiveGrants computes the materialized grant table of one role by
// breadth-first levels over the parent hierarchy. The visited set makes the
// computation terminate on cyclic hierarchies; the level aggregation makes
// the result independent of parent enumeration order.
func (e *Engine) effectiveGrants(role string) map[string]GrantInfo {
	out := map[string]GrantInfo{}
	visited := map[string]struct{}{role: {}}
	level := []string{role}
	for distance := 0; len(level) > 0; distance++ {
		// Aggregate declarations of this level; deny wins within a level.
		levelDeny := map[string]bool{}
		levelAllow := map[string]bool{}
		var next []string
		for _, r := range level {
			for k, effect := range e.grants {
				if k.role != r {
					continue
				}
				if effect == Deny {
					levelDeny[k.tag] = true
				} else {
					levelAllow[k.tag] = true
				}
			}
			for p := range e.roleParents[r] {
				if _, ok := visited[p]; !ok {
					visited[p] = struct{}{}
					next = append(next, p)
				}
			}
		}
		for tag := range levelDeny {
			if _, resolved := out[tag]; !resolved {
				out[tag] = GrantInfo{Effect: Deny, Distance: distance}
			}
		}
		for tag := range levelAllow {
			if _, resolved := out[tag]; !resolved {
				out[tag] = GrantInfo{Effect: Allow, Distance: distance}
			}
		}
		level = next
	}
	return out
}
