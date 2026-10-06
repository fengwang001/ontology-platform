package gradaudit

import "sort"

// assignment maps a leaf requirement code to the options assigned to it.
type assignment map[string][]option

// enumeration holds every feasible assignment and plan-derived indexing.
type enumeration struct {
	view       *countedView
	all        []assignment
	leaves     []*Requirement
	leafByCode map[string]*Requirement
	shared     map[[2]string]bool
}

func enumerateAssignments(view *countedView) *enumeration {
	en := &enumeration{
		view:       view,
		shared:     map[[2]string]bool{},
		leafByCode: map[string]*Requirement{},
	}
	for _, p := range view.plan.SharedPairs {
		a, b := p[0], p[1]
		if a > b {
			a, b = b, a
		}
		en.shared[[2]string{a, b}] = true
	}
	en.leaves = collectLeaves(view.plan.Root)
	for _, l := range en.leaves {
		en.leafByCode[l.Code] = l
	}

	eligible := map[*Requirement][]option{}
	for _, leaf := range en.leaves {
		set := map[string]bool{}
		for _, c := range leaf.Courses {
			set[c] = true
		}
		for _, o := range view.options {
			if set[o.course] {
				eligible[leaf] = append(eligible[leaf], o)
			}
		}
	}

	cur := assignment{}
	recLeaves := map[string]map[string]bool{}   // record ID -> leaf codes using it
	leafCourses := map[string]map[string]bool{} // leaf code -> courses
	en.dfs(0, eligible, cur, recLeaves, leafCourses)

	sort.Slice(en.all, func(i, j int) bool { return assignmentKey(en.all[i]) < assignmentKey(en.all[j]) })
	return en
}

func collectLeaves(r *Requirement) []*Requirement {
	if r.Leaf {
		return []*Requirement{r}
	}
	var out []*Requirement
	for _, c := range r.Children {
		out = append(out, collectLeaves(c)...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

func (en *enumeration) dfs(idx int, eligible map[*Requirement][]option, cur assignment, recLeaves map[string]map[string]bool, leafCourses map[string]map[string]bool) {
	if idx == len(en.leaves) {
		cp := assignment{}
		for k, v := range cur {
			cp[k] = append([]option(nil), v...)
		}
		en.all = append(en.all, cp)
		return
	}
	leaf := en.leaves[idx]
	opts := eligible[leaf]

	var branch func(start int, pick []option)
	branch = func(start int, pick []option) {
		// Emit the current (possibly empty) selection for this leaf.
		if len(pick) > 0 {
			cur[leaf.Code] = append([]option(nil), pick...)
		}
		en.dfs(idx+1, eligible, cloneAssignment(cur), cloneRecLeaves(recLeaves), cloneLeafCourses(leafCourses))
		delete(cur, leaf.Code)

		for i := start; i < len(opts); i++ {
			o := opts[i]
			if leafCourses[leaf.Code] != nil && leafCourses[leaf.Code][o.course] {
				continue // one course once per leaf
			}
			users := recLeaves[o.rec.ID]
			if len(users) > 0 && !en.allSharedWith(leaf.Code, users) {
				continue
			}
			if recLeaves[o.rec.ID] == nil {
				recLeaves[o.rec.ID] = map[string]bool{}
			}
			recLeaves[o.rec.ID][leaf.Code] = true
			if leafCourses[leaf.Code] == nil {
				leafCourses[leaf.Code] = map[string]bool{}
			}
			leafCourses[leaf.Code][o.course] = true

			next := append(append([]option(nil), pick...), o)
			branch(i+1, next)

			delete(leafCourses[leaf.Code], o.course)
			delete(recLeaves[o.rec.ID], leaf.Code)
			if len(recLeaves[o.rec.ID]) == 0 {
				delete(recLeaves, o.rec.ID)
			}
		}
	}
	branch(0, nil)
}

func (en *enumeration) allSharedWith(code string, others map[string]bool) bool {
	for other := range others {
		a, b := code, other
		if a > b {
			a, b = b, a
		}
		if !en.shared[[2]string{a, b}] {
			return false
		}
	}
	return true
}

func cloneAssignment(a assignment) assignment {
	cp := assignment{}
	for k, v := range a {
		cp[k] = append([]option(nil), v...)
	}
	return cp
}

func cloneRecLeaves(m map[string]map[string]bool) map[string]map[string]bool {
	cp := map[string]map[string]bool{}
	for k, v := range m {
		cp[k] = map[string]bool{}
		for x := range v {
			cp[k][x] = true
		}
	}
	return cp
}

func cloneLeafCourses(m map[string]map[string]bool) map[string]map[string]bool {
	return cloneRecLeaves(m)
}

// leafMet reports whether a leaf meets credit/course thresholds.
func leafMet(leaf *Requirement, os []option) bool {
	courses := map[string]bool{}
	credit := 0.0
	for _, o := range os {
		courses[o.course] = true
		credit += o.credit
	}
	return credit+1e-9 >= leaf.MinCredit && len(courses) >= leaf.MinCourses
}

func (en *enumeration) satisfiedMap(a assignment) map[*Requirement]bool {
	m := map[*Requirement]bool{}
	for _, leaf := range en.leaves {
		m[leaf] = leafMet(leaf, a[leaf.Code])
	}
	return m
}

func (en *enumeration) rootPass(a assignment) bool {
	return en.view.plan.Root.Satisfied(en.satisfiedMap(a))
}

// scoreVector orders leaves deterministically by code and records satisfaction.
func (en *enumeration) scoreVector(a assignment) []bool {
	m := en.satisfiedMap(a)
	v := make([]bool, len(en.leaves))
	for i, l := range en.leaves {
		v[i] = m[l]
	}
	return v
}

func vectorLess(a, b []bool) bool {
	for i := range a {
		if a[i] != b[i] {
			return !a[i] && b[i] // satisfied=true is "greater"
		}
	}
	return false
}

// canonical returns the lexicographically greatest assignment by leaf-code
// satisfaction vector, then by stable assignment key.
func (en *enumeration) canonical(passOnly bool) assignment {
	var best assignment
	var bestVec []bool
	for _, a := range en.all {
		if passOnly && !en.rootPass(a) {
			continue
		}
		v := en.scoreVector(a)
		if best == nil || vectorLess(bestVec, v) ||
			(eqVector(bestVec, v) && assignmentKey(best) < assignmentKey(a)) {
			best, bestVec = a, v
		}
	}
	return best
}

func eqVector(a, b []bool) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// alwaysUnsatisfied returns leaf codes that fail in every feasible assignment.
func (en *enumeration) alwaysUnsatisfied() []string {
	ever := map[string]bool{}
	for _, a := range en.all {
		m := en.satisfiedMap(a)
		for _, l := range en.leaves {
			if m[l] {
				ever[l.Code] = true
			}
		}
	}
	var out []string
	for _, l := range en.leaves {
		if !ever[l.Code] {
			out = append(out, l.Code)
		}
	}
	sort.Strings(out)
	return out
}

// bestGapFor picks the most favorable gap for a node among assignments whose
// leaf-prefix up to the failing node is considered; per the spec, the gap uses
// the assignment that maximizes this node's progress.
func (en *enumeration) bestGapFor(node *Requirement) (ReqGap, assignment) {
	var bestGap *ReqGap
	var bestA assignment
	for _, a := range en.all {
		g := gapOf(en.view.plan.Root, node, a, en)
		if bestGap == nil || gapLess(g, *bestGap) {
			g2 := g
			bestGap = &g2
			bestA = a
		}
	}
	if bestGap == nil {
		return ReqGap{Code: node.Code}, nil
	}
	return *bestGap, bestA
}

func gapOf(root, node *Requirement, a assignment, en *enumeration) ReqGap {
	if node.Leaf {
		os := a[node.Code]
		courses := map[string]bool{}
		credit := 0.0
		for _, o := range os {
			courses[o.course] = true
			credit += o.credit
		}
		g := ReqGap{Code: node.Code, Leaf: true}
		if credit+1e-9 < node.MinCredit {
			g.CreditGap = node.MinCredit - credit
		}
		if len(courses) < node.MinCourses {
			g.CourseGap = node.MinCourses - len(courses)
		}
		return g
	}
	m := en.satisfiedMap(a)
	n := 0
	for _, c := range node.Children {
		if c.Satisfied(m) {
			n++
		}
	}
	need := node.MinChildren
	if need <= 0 || need > len(node.Children) {
		need = len(node.Children)
	}
	g := ReqGap{Code: node.Code, Leaf: false}
	if n < need {
		g.ChildrenGap = need - n
	}
	return g
}

func gapLess(a, b ReqGap) bool {
	if a.Leaf {
		if a.CreditGap != b.CreditGap {
			return a.CreditGap < b.CreditGap
		}
		return a.CourseGap < b.CourseGap
	}
	return a.ChildrenGap < b.ChildrenGap
}

func assignmentKey(a assignment) string {
	codes := make([]string, 0, len(a))
	for c := range a {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	key := ""
	for _, c := range codes {
		parts := make([]string, 0, len(a[c]))
		for _, o := range a[c] {
			parts = append(parts, o.rec.ID+"/"+o.course)
		}
		sort.Strings(parts)
		key += c + ":"
		for _, p := range parts {
			key += p + ","
		}
		key += "|"
	}
	return key
}
