package semver

import "sort"

// commonGatedTriples 返回被每个 group 都放行的三元组交集。
func commonGatedTriples(groups []group) map[triple]bool {
	if len(groups) == 0 {
		return nil
	}
	common := map[triple]bool{}
	for t := range groups[0].gates {
		common[t] = true
	}
	for _, g := range groups[1:] {
		for t := range common {
			if !g.gates[t] {
				delete(common, t)
			}
		}
	}
	return common
}

// sortedTriples 把三元组集合按优先级排序，保证求交结果稳定可复现。
func sortedTriples(set map[triple]bool) []triple {
	out := make([]triple, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].cmp(out[j]) < 0 })
	return out
}

// pairwiseConflict 在约束中找出一对互相冲突的约束；没有则返回 false。
// owners[i] 是 all[i] 所属原始 group 的下标：同一 group 内门槛为并集，
// 不同 group 间门槛为交集。
func pairwiseConflict(all []Constraint, owners []int) (Constraint, Constraint, bool) {
	for i := 0; i < len(all); i++ {
		for j := i + 1; j < len(all); j++ {
			if !pairSatisfiable(all[i], all[j], owners[i] == owners[j]) {
				return all[i], all[j], true
			}
		}
	}
	return Constraint{}, Constraint{}, false
}

func pairSatisfiable(a, b Constraint, sameGroup bool) bool {
	var groups []group
	if sameGroup {
		gates := mergeGates(gateSet(a), gateSet(b))
		groups = []group{{constraints: []Constraint{a, b}, gates: gates}}
	} else {
		groups = []group{
			{constraints: []Constraint{a}, gates: gateSet(a)},
			{constraints: []Constraint{b}, gates: gateSet(b)},
		}
	}
	_, ok := feasibilityWitness(groups, []Constraint{a, b})
	return ok
}

func gateSet(c Constraint) map[triple]bool {
	if c.gate {
		return map[triple]bool{versionTriple(c.Ver): true}
	}
	return map[triple]bool{}
}

func mergeGates(a, b map[triple]bool) map[triple]bool {
	out := map[triple]bool{}
	for t := range a {
		out[t] = true
	}
	for t := range b {
		out[t] = true
	}
	return out
}
