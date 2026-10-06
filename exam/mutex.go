package exam

// Mutex groups form an undirected bipartite graph between questions and
// groups; two questions are mutually exclusive iff they lie in the same
// connected component (the transitive closure over shared groups).
//
// The engine maintains the connected components incrementally so that a
// conflict check never scans the question bank or the group table:
//
//   - addMembership merges the components of the group's members; the
//     relabeling cost is proportional to the smaller merged components.
//   - removeMembership recomputes components only inside the affected
//     component; the cost is proportional to that component's size.
//   - conflictsWith / mutexAmong only read comp[q] for the candidate and
//     the paper's own questions: O(paper size) map reads, independent of
//     the total number of questions and groups. checkOps counts those
//     reads so tests can assert the bound.

// compOf returns the component id of q, lazily creating a singleton.
func (e *Engine) compOf(q string) int {
	id, ok := e.comp[q]
	if !ok {
		id = e.nextComp
		e.nextComp++
		e.comp[q] = id
		e.compMembers[id] = map[string]bool{q: true}
	}
	return id
}

// addMembership records q in group g and merges the affected components.
func (e *Engine) addMembership(q, g string) {
	if e.groups[g] == nil {
		e.groups[g] = map[string]bool{}
	}
	e.groups[g][q] = true
	if e.qgroups[q] == nil {
		e.qgroups[q] = map[string]bool{}
	}
	e.qgroups[q][g] = true

	comps := map[int]bool{}
	for m := range e.groups[g] {
		comps[e.compOf(m)] = true
	}
	if len(comps) <= 1 {
		return
	}
	survivor, size := -1, -1
	for c := range comps {
		if n := len(e.compMembers[c]); n > size {
			survivor, size = c, n
		}
	}
	for c := range comps {
		if c == survivor {
			continue
		}
		for m := range e.compMembers[c] {
			e.comp[m] = survivor
			e.compMembers[survivor][m] = true
		}
		delete(e.compMembers, c)
	}
}

// removeMembership drops q from group g and recomputes the connected
// components inside q's old component, which may split it.
func (e *Engine) removeMembership(q, g string) {
	delete(e.groups[g], q)
	if len(e.groups[g]) == 0 {
		delete(e.groups, g)
	}
	delete(e.qgroups[q], g)
	if len(e.qgroups[q]) == 0 {
		delete(e.qgroups, q)
	}

	old := e.compOf(q)
	members := e.compMembers[old]
	seen := map[string]bool{}
	var parts []map[string]bool
	for seed := range members {
		if seen[seed] {
			continue
		}
		part := map[string]bool{}
		queue := []string{seed}
		seen[seed] = true
		for len(queue) > 0 {
			x := queue[0]
			queue = queue[1:]
			part[x] = true
			for grp := range e.qgroups[x] {
				for y := range e.groups[grp] {
					if members[y] && !seen[y] {
						seen[y] = true
						queue = append(queue, y)
					}
				}
			}
		}
		parts = append(parts, part)
	}
	if len(parts) <= 1 {
		return // removal did not split the component
	}
	delete(e.compMembers, old)
	for i, part := range parts {
		id := old
		if i > 0 {
			id = e.nextComp
			e.nextComp++
		}
		e.compMembers[id] = part
		for m := range part {
			e.comp[m] = id
		}
	}
}

// conflictsWith reports whether candidate is mutually exclusive with any of
// existing, returning the first conflicting question. Cost: len(existing)
// map reads, independent of question-bank and group-table sizes.
func (e *Engine) conflictsWith(candidate string, existing []string) (string, bool) {
	c := e.compOf(candidate)
	for _, q := range existing {
		e.checkOps++
		if e.compOf(q) == c {
			return q, true
		}
	}
	return "", false
}

// mutexAmong checks a whole question set for pairwise conflicts.
func (e *Engine) mutexAmong(paper string, ids []string) *Error {
	seen := map[int]string{}
	for _, id := range ids {
		c := e.compOf(id)
		if other, ok := seen[c]; ok {
			return newError(ErrMutexConflict, paper, sortedPair(other, id),
				"questions %s and %s are mutually exclusive", other, id)
		}
		seen[c] = id
	}
	return nil
}

func sortedPair(a, b string) []string {
	if a > b {
		a, b = b, a
	}
	return []string{a, b}
}
