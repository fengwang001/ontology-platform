package audit

// 本文件负责归入判定：穷举每个计入项的替换选择与叶子归入方式，
// 计算要求树满足情况，并在不通过时给出确定性归因与最有利缺口。
//
// 穷举空间对单个学生为 Π(替换选项+1) × Π(可归入叶子+1)，
// 仅依赖该学生自己的课程项与方案树大小，与其他学生数据无关。

import "sort"

// assignment 一种归入方式：叶子 ID -> 归入的课程身份集合。
type assignment map[string]map[string]bool

type assignResult struct {
	rootSatisfied bool
	assignment    assignment
	score         int
	leafCredits   map[string]float64
	leafCourses   map[string]int
	sat           map[string]bool
	itemLeaf      map[int][]string // item 序号 -> 归入叶子（空表示未归入）
	itemIdentity  map[int]string
	itemCredit    map[int]float64
}

// choice 穷举状态中的一个决定。
type choice struct {
	itemIndex int
	sub       *Substitution // nil = 不替换
	leafIDs   []string      // 空 = 不归入；多个 = 显式允许重复计入的叶子对/组
	identity  string
	credits   float64
}

func searchAssignments(ctx *studentContext) []*assignResult {
	plan := ctx.plan
	leaves := collectLeaves(plan.Root)
	leafByID := map[string]*Requirement{}
	leavesByCourse := map[string][]*Requirement{}
	for _, l := range leaves {
		leafByID[l.ID] = l
		for _, c := range l.Courses {
			leavesByCourse[c] = append(leavesByCourse[c], l)
		}
	}
	for c := range leavesByCourse {
		sort.Slice(leavesByCourse[c], func(i, j int) bool {
			return leavesByCourse[c][i].ID < leavesByCourse[c][j].ID
		})
	}

	var results []*assignResult
	cur := make([]choice, len(ctx.items))

	var dfs func(idx int)
	dfs = func(idx int) {
		if idx == len(ctx.items) {
			results = append(results, evalChoices(ctx, cur, leafByID))
			return
		}
		it := ctx.items[idx]
		opts := ctx.subOptions[it.sourceID]
		if opts == nil {
			opts = []*Substitution{nil}
		}
		seenIdent := map[string]bool{}
		for _, sub := range opts {
			ident, credit := ctx.resolveOption(it, sub)
			if seenIdent[ident] {
				continue
			}
			seenIdent[ident] = true
			// 不归入任何叶子。
			cur[idx] = choice{itemIndex: idx, sub: sub, identity: ident, credits: credit}
			dfs(idx + 1)
			// 归入每个可归入叶子（leavesByCourse 已按叶子 ID 排序）。
			cand := leavesByCourse[ident]
			for i, leaf := range cand {
				cur[idx] = choice{itemIndex: idx, sub: sub, leafIDs: []string{leaf.ID}, identity: ident, credits: credit}
				dfs(idx + 1)
				// 与其它可归入叶子中显式允许重复计入的叶子组合。
				for j := i + 1; j < len(cand); j++ {
					a, b := leaf.ID, cand[j].ID
					if a > b {
						a, b = b, a
					}
					if plan.SharedCredit[[2]string{a, b}] {
						cur[idx] = choice{itemIndex: idx, sub: sub,
							leafIDs: []string{leaf.ID, cand[j].ID}, identity: ident, credits: credit}
						dfs(idx + 1)
					}
				}
			}
		}
	}
	dfs(0)
	return results
}

func evalChoices(ctx *studentContext, choices []choice, leafByID map[string]*Requirement) *assignResult {
	as := assignment{}
	// 课程身份 -> 已归入叶子集合（用于重复计入校验）。
	used := map[string]map[string]bool{}
	r := &assignResult{
		assignment:   as,
		leafCredits:  map[string]float64{},
		leafCourses:  map[string]int{},
		sat:          map[string]bool{},
		itemLeaf:     map[int][]string{},
		itemIdentity: map[int]string{},
		itemCredit:   map[int]float64{},
	}

	for _, ch := range choices {
		r.itemLeaf[ch.itemIndex] = ch.leafIDs
		r.itemIdentity[ch.itemIndex] = ch.identity
		r.itemCredit[ch.itemIndex] = ch.credits
		if len(ch.leafIDs) == 0 {
			continue
		}
		for _, leafID := range ch.leafIDs {
			if as[leafID] != nil && as[leafID][ch.identity] {
				continue // 同一身份重复归入同一叶子：只计一次。
			}
			if otherLeaves := used[ch.identity]; len(otherLeaves) > 0 && !sharedAllowed(ctx.plan, otherLeaves, leafID) {
				continue // 已归入其他叶子且方案未声明可重复计入。
			}
			if as[leafID] == nil {
				as[leafID] = map[string]bool{}
			}
			if used[ch.identity] == nil {
				used[ch.identity] = map[string]bool{}
			}
			as[leafID][ch.identity] = true
			used[ch.identity][leafID] = true
			r.leafCredits[leafID] += ch.credits
			r.leafCourses[leafID]++
		}
	}

	r.score = evalTree(ctx.plan.Root, r)
	r.rootSatisfied = r.sat[ctx.plan.Root.ID]
	return r
}

func sharedAllowed(p *PlanVersion, leaves map[string]bool, leafID string) bool {
	for lid := range leaves {
		if lid == leafID {
			continue
		}
		a, b := lid, leafID
		if a > b {
			a, b = b, a
		}
		if p.SharedCredit[[2]string{a, b}] {
			return true
		}
	}
	return false
}

func evalTree(req *Requirement, r *assignResult) int {
	if req.Kind == ReqLeaf {
		ok := ge0(r.leafCredits[req.ID], req.MinCredits) && r.leafCourses[req.ID] >= req.MinCourses
		r.sat[req.ID] = ok
		if ok {
			return 1
		}
		return 0
	}
	satisfied := 0
	total := 0
	for _, c := range req.Children {
		total += evalTree(c, r)
		if r.sat[c.ID] {
			satisfied++
		}
	}
	ok := satisfied >= req.MinChildren
	r.sat[req.ID] = ok
	if ok {
		return total + 1
	}
	return total
}

// betterResult：满足节点数多者优；并列时按归入方式的规范编码取字典序较小者。
func betterResult(a, b *assignResult, ctx *studentContext) bool {
	if a.score != b.score {
		return a.score > b.score
	}
	return encodeAssignment(a, ctx) < encodeAssignment(b, ctx)
}

func encodeAssignment(r *assignResult, ctx *studentContext) string {
	parts := make([]string, 0, len(ctx.items))
	for _, it := range ctx.items {
		leaves := r.itemLeaf[indexOf(ctx.items, it)]
		leaf := "-"
		if len(leaves) > 0 {
			leaf = joinStrings(leaves, ",")
		}
		parts = append(parts, it.sourceID+"->"+leaf)
	}
	return joinStrings(parts, "|")
}

func indexOf(items []*countedItem, target *countedItem) int {
	for i, it := range items {
		if it == target {
			return i
		}
	}
	return -1
}

func joinStrings(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}

// impossibleReqs 返回在所有归入方式中都无法满足的要求集合。
func impossibleReqs(results []*assignResult) map[string]bool {
	out := map[string]bool{}
	if len(results) == 0 {
		return out
	}
	for id := range results[0].sat {
		out[id] = true
	}
	for _, r := range results[1:] {
		for id := range out {
			if r.sat[id] {
				delete(out, id)
			}
		}
	}
	return out
}

// bestGapFor 返回某叶子在所有归入方式中最有利（缺口最小）的学分与门数缺口。
func bestGapFor(results []*assignResult, leaf *Requirement) (creditGap float64, courseGap int) {
	creditGap = leaf.MinCredits
	courseGap = leaf.MinCourses
	for _, r := range results {
		cg := leaf.MinCredits - r.leafCredits[leaf.ID]
		if cg < 0 {
			cg = 0
		}
		ng := leaf.MinCourses - r.leafCourses[leaf.ID]
		if ng < 0 {
			ng = 0
		}
		if cg < creditGap {
			creditGap = cg
		}
		if ng < courseGap {
			courseGap = ng
		}
	}
	if creditGap < 0 {
		creditGap = 0
	}
	return
}

func collectLeaves(root *Requirement) []*Requirement {
	var leaves []*Requirement
	var walk func(r *Requirement)
	walk = func(r *Requirement) {
		if r.Kind == ReqLeaf {
			leaves = append(leaves, r)
			return
		}
		for _, c := range r.Children {
			walk(c)
		}
	}
	walk(root)
	sort.Slice(leaves, func(i, j int) bool { return leaves[i].ID < leaves[j].ID })
	return leaves
}

func collectAll(root *Requirement) []*Requirement {
	var all []*Requirement
	var walk func(r *Requirement)
	walk = func(r *Requirement) {
		all = append(all, r)
		for _, c := range r.Children {
			walk(c)
		}
	}
	walk(root)
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	return all
}
