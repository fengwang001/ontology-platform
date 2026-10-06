package audit

// 本文件负责审核结论：要求树判定、毕业附加条件（总学分、加权平均、
// 未结清不及格必修）与确定性归因。

import "sort"

// Attrib 未满足要求的归因。
type Attrib struct {
	ReqID     string
	CreditGap float64
	CourseGap int
	// Impossible 表示该要求在所有归入方式中均无法满足。
	Impossible bool
}

// ConditionFail 一项未满足的毕业附加条件。
type ConditionFail struct {
	Kind    string // "total_credit" | "gpa" | "uncleared_required_fail"
	Detail  string
	Course  string
	Have    float64
	Require float64
}

// AuditResult 一次审核的完整结论。
type AuditResult struct {
	Pass           bool
	Student        string
	PlanID         string
	PlanVersion    int
	TotalCredits   float64
	GPA            float64
	Attrib         *Attrib
	ConditionFails []ConditionFail
	// Assignment 规范最优归入：叶子 ID -> 归入课程身份。
	Assignment     map[string][]string
	CountedCourses []string
}

func runAudit(ctx *studentContext) *AuditResult {
	res := &AuditResult{
		Student:     ctx.student,
		PlanID:      ctx.plan.PlanID,
		PlanVersion: ctx.plan.Version,
	}

	all := searchAssignments(ctx)
	if len(all) == 0 {
		// 没有任何计入项仍需给出结论。
		empty := evalChoices(ctx, nil, map[string]*Requirement{})
		all = []*assignResult{empty}
	}

	// 是否存在某种归入方式使根要求满足。
	rootPass := false
	for _, r := range all {
		if r.rootSatisfied {
			rootPass = true
			break
		}
	}

	// 规范最优归入用于确定性展示总学分、平均分与归入明细。
	best := all[0]
	for _, r := range all[1:] {
		if betterResult(r, best, ctx) {
			best = r
		}
	}

	res.TotalCredits = sumCredits(ctx, best)
	res.GPA = weightedGPA(ctx, best)
	res.Assignment = renderAssignment(ctx, best)
	res.CountedCourses = countedList(ctx, best)

	if !rootPass {
		res.Attrib = attribute(ctx, all)
	}

	// 附加条件：总学分。
	if !ge0(res.TotalCredits, ctx.plan.MinTotalCredit) {
		res.ConditionFails = append(res.ConditionFails, ConditionFail{
			Kind: "total_credit", Detail: "计入总学分不足",
			Have: res.TotalCredits, Require: ctx.plan.MinTotalCredit,
		})
	}
	// 附加条件：加权平均成绩下限（含等号）。
	if !ge0(res.GPA, ctx.plan.MinGPA) {
		res.ConditionFails = append(res.ConditionFails, ConditionFail{
			Kind: "gpa", Detail: "加权平均成绩低于下限",
			Have: res.GPA, Require: ctx.plan.MinGPA,
		})
	}
	// 附加条件：无未结清不及格必修。
	for _, c := range unclearedRequiredFails(ctx, best) {
		res.ConditionFails = append(res.ConditionFails, ConditionFail{
			Kind: "uncleared_required_fail", Course: c,
			Detail: "必修课程存在不及格且无后续及格修读",
		})
	}

	res.Pass = rootPass && len(res.ConditionFails) == 0
	return res
}

// attribute 在所有归入方式都无法满足的要求集合中取编号最小者；
// 缺口按对该要求最有利的归入方式计算。
func attribute(ctx *studentContext, results []*assignResult) *Attrib {
	impossible := impossibleReqs(results)
	all := collectAll(ctx.plan.Root)
	byID := map[string]*Requirement{}
	for _, r := range all {
		byID[r.ID] = r
	}
	var target *Requirement
	for _, r := range all {
		if impossible[r.ID] {
			target = r
			break
		}
	}
	if target == nil {
		return nil
	}
	a := &Attrib{ReqID: target.ID, Impossible: true}
	if target.Kind == ReqLeaf {
		a.CreditGap, a.CourseGap = bestGapFor(results, target)
	} else {
		// 内部要求本身无学分/门数；归并到其必然无法满足的叶子后代。
		var leaves []*Requirement
		var walk func(r *Requirement)
		walk = func(r *Requirement) {
			if r.Kind == ReqLeaf {
				if impossible[r.ID] {
					leaves = append(leaves, r)
				}
				return
			}
			for _, c := range r.Children {
				walk(c)
			}
		}
		walk(target)
		for _, l := range leaves {
			cg, ng := bestGapFor(results, l)
			a.CreditGap += cg
			a.CourseGap += ng
		}
	}
	return a
}

// sumCredits 计入总学分：每个计入项按其在该归入方式下的有效学分之和
// （未归入的课程仍以原始学分计入毕业总学分）。
func sumCredits(ctx *studentContext, r *assignResult) float64 {
	var total float64
	for _, it := range ctx.items {
		idx := indexOf(ctx.items, it)
		if c, ok := r.itemCredit[idx]; ok {
			total += c
		} else {
			total += it.credits
		}
	}
	return total
}

func effectiveItemCredit(ctx *studentContext, r *assignResult, it *countedItem) float64 {
	idx := indexOf(ctx.items, it)
	if c, ok := r.itemCredit[idx]; ok {
		return c
	}
	return it.credits
}

// weightedGPA 按计入记录的学分加权平均；无计入学分时返回 0。
func weightedGPA(ctx *studentContext, r *assignResult) float64 {
	var sum, weight float64
	for _, it := range ctx.items {
		credit := effectiveItemCredit(ctx, r, it)
		sum += it.grade * credit
		weight += credit
	}
	if weight <= eps {
		return 0
	}
	return sum / weight
}

func renderAssignment(ctx *studentContext, r *assignResult) map[string][]string {
	out := map[string][]string{}
	for leafID, idents := range r.assignment {
		list := make([]string, 0, len(idents))
		for id := range idents {
			list = append(list, id)
		}
		sort.Strings(list)
		out[leafID] = list
	}
	return out
}

func countedList(ctx *studentContext, r *assignResult) []string {
	out := make([]string, 0, len(ctx.items))
	for _, it := range ctx.items {
		idx := indexOf(ctx.items, it)
		if id, ok := r.itemIdentity[idx]; ok {
			out = append(out, id)
		} else {
			out = append(out, it.identity)
		}
	}
	sort.Strings(out)
	return out
}

// unclearedRequiredFails 判定未结清的不及格必修课程：
// 课程可归入某必修类叶子，且存在不及格修读、又没有学期更晚（含同学期）的及格修读。
func unclearedRequiredFails(ctx *studentContext, chosen *assignResult) []string {
	requiredCourses := map[string]bool{}
	for _, leaf := range collectLeaves(ctx.plan.Root) {
		if !leaf.Required {
			continue
		}
		for _, c := range leaf.Courses {
			requiredCourses[c] = true
		}
	}
	// 课程 -> 全部未撤销修读。
	grades := map[string][]rawAttempt{}
	for _, a := range ctx.attempts {
		grades[a.course] = append(grades[a.course], a)
	}
	var bad []string
	for course := range requiredCourses {
		var failSem string
		hasFail := false
		cleared := false
		for _, a := range grades[course] {
			if !ge0(a.grade, ctx.plan.PassLine) {
				if !hasFail || semesterLE(a.semester, failSem) {
					hasFail, failSem = true, a.semester
				}
			}
		}
		if !hasFail {
			continue
		}
		for _, a := range grades[course] {
			if ge0(a.grade, ctx.plan.PassLine) && semesterLE(failSem, a.semester) {
				cleared = true
			}
		}
		if !cleared {
			bad = append(bad, course)
		}
	}
	sort.Strings(bad)
	return bad
}
