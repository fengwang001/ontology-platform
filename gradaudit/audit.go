package gradaudit

import "sort"

// runAudit computes the full audit verdict from a counted view.
func runAudit(studentID string, st *studentState, plan *PlanVersion, view *countedView) *AuditResult {
	en := enumerateAssignments(view)
	res := &AuditResult{
		StudentID:  studentID,
		PlanID:     plan.ID,
		Additional: []AdditionalFailure{},
	}

	var canon assignment
	passing := false
	for _, a := range en.all {
		if en.rootPass(a) {
			passing = true
			break
		}
	}
	if passing {
		res.TreeSatisfied = true
		canon = en.canonical(true)
	} else {
		canon = en.canonical(false)
		res.Attribution = attribute(en)
	}

	// Total counted credits and weighted GPA are computed from the canonical
	// assignment: every record used at least once counts, substituted credit
	// uses the option actually claimed; records unused there still count as
	// counted enrollments at native credit (they remain valid passing records).
	totalCredit := 0.0
	weighted := 0.0
	weight := 0.0
	// A surviving record contributes its best claimable credit (native or via
	// substitution), exactly once; credit is never multiplied by assignment.
	bestClaim := map[string]float64{}
	for _, o := range view.options {
		if o.credit > bestClaim[o.rec.ID] {
			bestClaim[o.rec.ID] = o.credit
		}
	}
	for recID, credit := range bestClaim {
		var score float64
		for _, o := range view.options {
			if o.rec.ID == recID {
				score = o.rec.Score
				break
			}
		}
		totalCredit += credit
		weighted += score * credit
		weight += credit
	}
	res.CountedCredit = totalCredit
	if weight > 0 {
		res.WeightedGPA = weighted / weight
	}

	if totalCredit+1e-9 < plan.TotalCredit {
		res.Additional = append(res.Additional, AdditionalFailure{
			Kind:    "total_credit",
			Missing: plan.TotalCredit - totalCredit,
		})
	}
	gpaOK := weight <= 0 && plan.MinGPA <= 0
	if weight > 0 && res.WeightedGPA+1e-9 >= plan.MinGPA {
		gpaOK = true
	}
	if !gpaOK {
		res.Additional = append(res.Additional, AdditionalFailure{
			Kind: "min_gpa",
		})
	}

	// Unresolved required-course failures: a course that at some point counted
	// toward a required leaf but has a failing enrollment with no later passing
	// enrollment (native or transfer, any record that survives the pass rules).
	for _, code := range requiredCoursesWithFailure(plan, st, canon, en) {
		res.Additional = append(res.Additional, AdditionalFailure{
			Kind:       "required_failure",
			CourseCode: code,
		})
	}

	res.Pass = res.TreeSatisfied && len(res.Additional) == 0
	return res
}

// attribute finds the smallest-coded node (leaf or internal) that cannot be
// satisfied under any assignment, reporting its most-favorable gap.
func attribute(en *enumeration) *Attribution {
	nodes := collectAll(en.view.plan.Root)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Code < nodes[j].Code })
	for _, node := range nodes {
		ever := false
		for _, a := range en.all {
			if node.Satisfied(en.satisfiedMap(a)) {
				ever = true
				break
			}
		}
		if !ever {
			gap, _ := en.bestGapFor(node)
			return &Attribution{Code: node.Code, Gap: gap}
		}
	}
	return nil
}

func collectAll(r *Requirement) []*Requirement {
	out := []*Requirement{r}
	if !r.Leaf {
		for _, c := range r.Children {
			out = append(out, collectAll(c)...)
		}
	}
	return out
}

func requiredCoursesWithFailure(plan *PlanVersion, st *studentState, canon assignment, en *enumeration) []string {
	_ = canon
	requiredLeaf := map[string]bool{}
	for _, l := range en.leaves {
		if l.Required {
			requiredLeaf[l.Code] = true
		}
	}
	requiredCourses := map[string]bool{}
	for _, a := range en.all {
		for code, os := range a {
			if !requiredLeaf[code] {
				continue
			}
			for _, o := range os {
				requiredCourses[o.course] = true
			}
		}
	}
	var out []string
	for course := range requiredCourses {
		if hasUnresolvedFailure(course, st, plan) {
			out = append(out, course)
		}
	}
	sort.Strings(out)
	return out
}

func hasUnresolvedFailure(course string, st *studentState, plan *PlanVersion) bool {
	var failed []*Record
	maxPassSem := -1
	for _, r := range st.records {
		if r.Revoked || r.Course != course {
			continue
		}
		if r.Score < plan.PassScore-1e-9 {
			failed = append(failed, r)
		} else {
			if r.Semester > maxPassSem {
				maxPassSem = r.Semester
			}
		}
	}
	for _, f := range failed {
		if f.Semester > maxPassSem {
			return true
		}
	}
	return false
}
