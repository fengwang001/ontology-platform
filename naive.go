package audit

// 本文件是按业务规则独立写成的朴素参考模型：
// 直接对每个学生构造计入池、笛卡尔积枚举"替换选择 × 叶子归入"，
// 独立计算树满足、归因、总学分与加权平均。仅用于随机对照测试，
// 生产审核路径（runAudit/searchAssignments）不调用本文件的任何函数。

import "sort"

type nItem struct {
	source   string
	course   string
	credits  float64
	grade    float64
	semester string
	transfer bool
	subs     []*Substitution // 含 nil（不替换）
}

type nChoice struct {
	ident  string
	credit float64
	leaves []string
}

type naiveSnapshot struct {
	pass       bool
	attribID   string
	creditGap  float64
	courseGap  int
	hasAttrib  bool
	conditions []string
	total      float64
	gpa        float64
	assignment map[string][]string
}

func naiveAudit(eng *Engine, studentID string) (*naiveSnapshot, error) {
	eng.mu.RLock()
	defer eng.mu.RUnlock()

	s := eng.students[studentID]
	plan := eng.planFor(s)
	passLine := plan.PassLine

	// 1) 独立构造计入池：本校记录同课去重 + 转入学分按序截断。
	bestByCourse := map[string]*Record{}
	for _, rid := range eng.studentRecs[studentID] {
		rec := eng.records[rid]
		if rec.Revoked || rec.Grade+eps < passLine {
			continue
		}
		b := bestByCourse[rec.Course]
		if b == nil || betterAttempt(rec, b) {
			bestByCourse[rec.Course] = rec
		}
	}
	var items []*nItem
	for _, rec := range bestByCourse {
		var opts []*Substitution
		for _, sub := range eng.subIndex[s.PlanID][s.PlanVersion][rec.Course] {
			if semesterLE(sub.Effective, rec.Semester) {
				opts = append(opts, sub)
			}
		}
		sort.Slice(opts, func(i, j int) bool { return opts[i].ID < opts[j].ID })
		items = append(items, &nItem{
			source: rec.ID, course: rec.Course, credits: rec.Credits,
			grade: rec.Grade, semester: rec.Semester,
			subs: append([]*Substitution{nil}, opts...),
		})
	}
	var usedTr float64
	for _, tr := range eng.transfers[studentID] {
		if usedTr+tr.Credits > plan.TransferCap+eps {
			continue
		}
		usedTr += tr.Credits
		ti := &nItem{
			source: "transfer:" + itoa(tr.Seq), course: tr.Course, credits: tr.Credits,
			grade: tr.Grade, semester: tr.Semester, transfer: true,
			subs: []*Substitution{nil},
		}
		merged := false
		for k, existing := range items {
			if existing.course == tr.Course {
				if ti.grade > existing.grade ||
					(ti.grade == existing.grade && semesterLE(ti.semester, existing.semester)) ||
					(ti.grade == existing.grade && ti.semester == existing.semester && ti.source < existing.source) {
					items[k] = ti
				}
				merged = true
				break
			}
		}
		if !merged {
			items = append(items, ti)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].source < items[j].source })

	leaves := collectLeaves(plan.Root)
	eligible := map[string][]string{}
	for _, l := range leaves {
		for _, c := range l.Courses {
			eligible[c] = append(eligible[c], l.ID)
		}
	}
	for k := range eligible {
		sort.Strings(eligible[k])
	}

	type outcome struct {
		choices []nChoice
		sat     map[string]bool
		score   int
		lc      map[string]float64
		ln      map[string]int
	}
	var outcomes []outcome

	choices := make([]nChoice, len(items))

	resolve := func(it *nItem, sub *Substitution) (string, float64) {
		if sub == nil {
			return it.course, it.credits
		}
		cr := it.credits
		if tc := eng.courses[sub.To].Credits; tc < cr {
			cr = tc
		}
		return sub.To, cr
	}

	var enumerate func(idx int)
	enumerate = func(idx int) {
		if idx == len(items) {
			// 独立评估树。
			placed := map[string]map[string]float64{} // leaf -> ident -> credits
			identLeaves := map[string]map[string]bool{}
			for _, ch := range choices {
				if len(ch.leaves) == 0 {
					continue
				}
				for _, leaf := range ch.leaves {
					if prev := identLeaves[ch.ident]; prev != nil {
						if prev[leaf] {
							continue
						}
						ok := false
						for other := range prev {
							if other == leaf {
								continue
							}
							a, b := other, leaf
							if a > b {
								a, b = b, a
							}
							if plan.SharedCredit[[2]string{a, b}] {
								ok = true
							}
						}
						if !ok {
							continue
						}
					}
					if placed[leaf] == nil {
						placed[leaf] = map[string]float64{}
					}
					if _, dup := placed[leaf][ch.ident]; dup {
						continue
					}
					placed[leaf][ch.ident] = ch.credit
					if identLeaves[ch.ident] == nil {
						identLeaves[ch.ident] = map[string]bool{}
					}
					identLeaves[ch.ident][leaf] = true
				}
			}
			lc := map[string]float64{}
			ln := map[string]int{}
			for lid, ms := range placed {
				for _, cr := range ms {
					lc[lid] += cr
					ln[lid]++
				}
			}
			sat := map[string]bool{}
			var walk func(r *Requirement) int
			walk = func(r *Requirement) int {
				if r.Kind == ReqLeaf {
					ok := lc[r.ID]+eps >= r.MinCredits && ln[r.ID] >= r.MinCourses
					sat[r.ID] = ok
					if ok {
						return 1
					}
					return 0
				}
				cnt, tot := 0, 0
				for _, c := range r.Children {
					tot += walk(c)
					if sat[c.ID] {
						cnt++
					}
				}
				ok := cnt >= r.MinChildren
				sat[r.ID] = ok
				if ok {
					return tot + 1
				}
				return tot
			}
			score := walk(plan.Root)
			cp := make([]nChoice, len(choices))
			copy(cp, choices)
			lcCopy := map[string]float64{}
			lnCopy := map[string]int{}
			for k, v := range lc {
				lcCopy[k] = v
			}
			for k, v := range ln {
				lnCopy[k] = v
			}
			outcomes = append(outcomes, outcome{cp, sat, score, lcCopy, lnCopy})
			return
		}
		it := items[idx]
		seen := map[string]bool{}
		for _, sub := range it.subs {
			ident, cr := resolve(it, sub)
			if seen[ident] {
				continue
			}
			seen[ident] = true
			choices[idx] = nChoice{ident: ident, credit: cr}
			enumerate(idx + 1)
			cands := eligible[ident]
			for i, lid := range cands {
				choices[idx] = nChoice{ident: ident, credit: cr, leaves: []string{lid}}
				enumerate(idx + 1)
				for j := i + 1; j < len(cands); j++ {
					a, b := lid, cands[j]
					if a > b {
						a, b = b, a
					}
					if plan.SharedCredit[[2]string{a, b}] {
						choices[idx] = nChoice{ident: ident, credit: cr, leaves: []string{lid, cands[j]}}
						enumerate(idx + 1)
					}
				}
			}
		}
	}
	enumerate(0)

	if len(outcomes) == 0 {
		outcomes = append(outcomes, outcome{sat: map[string]bool{}, score: 0, lc: map[string]float64{}, ln: map[string]int{}})
	}

	rootPass := false
	for _, o := range outcomes {
		if o.sat[plan.Root.ID] {
			rootPass = true
			break
		}
	}

	// 规范最优：满足节点数最多，并列按 (source->leaf) 编码字典序。
	best := outcomes[0]
	bestEnc := nEncode(items, best.choices)
	for _, o := range outcomes[1:] {
		enc := nEncode(items, o.choices)
		if o.score > best.score || (o.score == best.score && enc < bestEnc) {
			best, bestEnc = o, enc
		}
	}

	var total, sumG, w float64
	for i, it := range items {
		cr := best.choices[i].credit
		total += cr
		sumG += it.grade * cr
		w += cr
	}
	gpa := 0.0
	if w > eps {
		gpa = sumG / w
	}

	snap := &naiveSnapshot{total: total, gpa: gpa}

	if !rootPass {
		impossible := map[string]bool{}
		for _, r := range collectAll(plan.Root) {
			impossible[r.ID] = true
		}
		for _, o := range outcomes {
			for id := range impossible {
				if o.sat[id] {
					delete(impossible, id)
				}
			}
		}
		var target *Requirement
		for _, r := range collectAll(plan.Root) {
			if impossible[r.ID] {
				target = r
				break
			}
		}
		if target != nil {
			snap.hasAttrib = true
			snap.attribID = target.ID
			if target.Kind == ReqLeaf {
				cg, ng := target.MinCredits, target.MinCourses
				for _, o := range outcomes {
					haveC, haveN := o.lc[target.ID], o.ln[target.ID]
					dc, dn := target.MinCredits-haveC, target.MinCourses-haveN
					if dc < 0 {
						dc = 0
					}
					if dn < 0 {
						dn = 0
					}
					if dc < cg {
						cg = dc
					}
					if dn < ng {
						ng = dn
					}
				}
				snap.creditGap, snap.courseGap = cg, ng
			}
		}
	}

	if total+eps < plan.MinTotalCredit {
		snap.conditions = append(snap.conditions, "total_credit")
	}
	if gpa+eps < plan.MinGPA {
		snap.conditions = append(snap.conditions, "gpa")
	}
	for _, c := range nUnclearedRequired(eng, s, plan, passLine) {
		_ = c
		snap.conditions = append(snap.conditions, "uncleared_required_fail")
	}
	sort.Strings(snap.conditions)

	snap.assignment = map[string][]string{}
	for _, ch := range best.choices {
		for _, lid := range ch.leaves {
			snap.assignment[lid] = append(snap.assignment[lid], ch.ident)
		}
	}
	for k := range snap.assignment {
		sort.Strings(snap.assignment[k])
	}

	snap.pass = rootPass && len(snap.conditions) == 0
	return snap, nil
}

func nEncode(items []*nItem, choices []nChoice) string {
	out := ""
	for i, it := range items {
		if i > 0 {
			out += "|"
		}
		leaves := choices[i].leaves
		leaf := "-"
		if len(leaves) > 0 {
			leaf = joinStrings(leaves, ",")
		}
		out += it.source + "->" + leaf
	}
	return out
}

func nUnclearedRequired(eng *Engine, s *Student, plan *PlanVersion, passLine float64) []string {
	req := map[string]bool{}
	var walk func(r *Requirement)
	walk = func(r *Requirement) {
		if r.Kind == ReqLeaf {
			if r.Required {
				for _, c := range r.Courses {
					req[c] = true
				}
			}
			return
		}
		for _, c := range r.Children {
			walk(c)
		}
	}
	walk(plan.Root)
	grades := map[string][]rawAttempt{}
	for _, rid := range eng.studentRecs[s.ID] {
		rec := eng.records[rid]
		if rec.Revoked {
			continue
		}
		grades[rec.Course] = append(grades[rec.Course], rawAttempt{rec.Course, rec.Semester, rec.Grade})
	}
	var bad []string
	for course := range req {
		fail := ""
		hasFail := false
		for _, a := range grades[course] {
			if a.grade+eps < passLine {
				if !hasFail || semesterLE(a.semester, fail) {
					hasFail, fail = true, a.semester
				}
			}
		}
		if !hasFail {
			continue
		}
		cleared := false
		for _, a := range grades[course] {
			if a.grade+eps >= passLine && semesterLE(fail, a.semester) {
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

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
