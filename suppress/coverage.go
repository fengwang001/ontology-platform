package suppress

import "sort"

// coverageIndex 持有某快照上所有有效抑制作用的预计算结构。
//
// 对“本行/下一行”指令直接以目标行为键；禁用区间按标签配对为不相交半开区间
// [startLine, endLine)（endLine 取启用行，未闭合取 totalLines+1）；
// 全文件指令对所有行生效。命中判定对每个标签做 O(log) 查找，
// 每条诊断至多查两个标签（具体规则与“全部”），不随指令总数线性扫描。
type coverageIndex struct {
	totalLines int
	// points 按标签存“本行/下一行”指令的命中点，键为目标行；
	// 同一（标签，行）上可能有多个候选（同一行多条指令），按胜负键升序。
	points map[string][]pointHit
	// ranges 按标签存禁用区间（不相交、按起始行升序）。
	ranges map[string][]rng
	// wholeFile 按标签存全文件指令，已按胜负键升序。
	wholeFile map[string][]hit
}

// hit 是一个可命中诊断的候选（某条指令的某个标签）。
type hit struct {
	line  int
	order int
	label string
	kind  Kind
}

// winnerKey 为归属胜负键：指令行号升序、登记次序升序、
// 同一指令内具体规则标签（specificity=0）先于“全部”（specificity=1）。
type winnerKey struct {
	line        int
	order       int
	specificity int
}

func keyOf(h hit) winnerKey {
	spec := 0
	if h.label == AllTag {
		spec = 1
	}
	return winnerKey{line: h.line, order: h.order, specificity: spec}
}

func lessWinner(a, b winnerKey) bool {
	if a.line != b.line {
		return a.line < b.line
	}
	if a.order != b.order {
		return a.order < b.order
	}
	return a.specificity < b.specificity
}

type pointHit struct {
	line int
	hit  hit
}

// rng 是某个禁用标签的一条有效区间 [start, end)，携带开启它的命中候选。
type rng struct {
	start  int
	end    int
	opener hit
}

// labelSlot 唯一标识“某条已接受指令的某个标签”。
type labelSlot struct {
	order int
	label string
}

// buildCoverage 在快照上预计算覆盖结构，并同时产出区间配对阶段的标签级问题
// （重复禁用、孤立启用）与未闭合区间的指令级问题。
//
// 返回的 labelIssues 只含配对阶段产生的标签级问题；缺理由、无目标行、
// 未知规则与未使用在评估阶段按优先级并入。
func buildCoverage(snap snapshot) (*coverageIndex, map[labelSlot]IssueCode, []Issue) {
	idx := &coverageIndex{
		totalLines: snap.totalLines,
		points:     make(map[string][]pointHit),
		ranges:     make(map[string][]rng),
		wholeFile:  make(map[string][]hit),
	}

	labelIssues := make(map[labelSlot]IssueCode)
	open := make(map[string]hit) // 每个标签当前未闭合的禁用命中候选

	// 区间配对按（行号，登记次序）扫描：同一行上的多条指令按登记次序配对。
	dirs := append([]acceptedDirective(nil), snap.directives...)
	sort.SliceStable(dirs, func(i, j int) bool {
		if dirs[i].Line != dirs[j].Line {
			return dirs[i].Line < dirs[j].Line
		}
		return dirs[i].order < dirs[j].order
	})

	for _, d := range dirs {
		// 缺理由指令整体不生效：不参与任何作用，也不开启/关闭区间。
		if snap.requireReason && isBlank(d.Reason) {
			continue
		}
		switch d.Kind {
		case KindThisLine:
			addPoint(idx, snap, d, d.Line)
		case KindNextLine:
			// 无目标行：最后一行的下一行指令指令不生效（标签级问题在评估阶段补）。
			if d.Line < snap.totalLines {
				addPoint(idx, snap, d, d.Line+1)
			}
		case KindWholeFile:
			for _, l := range d.Labels {
				if !knownLabel(snap, l) {
					continue
				}
				h := hit{line: d.Line, order: d.order, label: l, kind: KindWholeFile}
				idx.wholeFile[l] = appendSortedHit(idx.wholeFile[l], h)
			}
		case KindDisable:
			for _, l := range d.Labels {
				if !knownLabel(snap, l) {
					continue
				}
				h := hit{line: d.Line, order: d.order, label: l, kind: KindDisable}
				if _, active := open[l]; active {
					labelIssues[labelSlot{order: d.order, label: l}] = IssueRepeatedDisable
					continue
				}
				open[l] = h
			}
		case KindEnable:
			for _, l := range d.Labels {
				if !knownLabel(snap, l) {
					continue
				}
				h, active := open[l]
				if !active {
					labelIssues[labelSlot{order: d.order, label: l}] = IssueOrphanEnable
					continue
				}
				// 半开区间 [禁用行, 启用行)：启用行本身不再被覆盖（取等边界）。
				idx.ranges[l] = append(idx.ranges[l], rng{start: h.line, end: d.Line, opener: h})
				delete(open, l)
			}
		}
	}

	// 到文件末尾仍未闭合：区间延续到最后一行（end=totalLines+1），另报未闭合。
	// 未闭合针对指令本身：一条禁用指令至多一条，与其各标签级问题并存。
	unclosedOrders := make(map[int]struct{})
	for l, h := range open {
		idx.ranges[l] = append(idx.ranges[l], rng{start: h.line, end: snap.totalLines + 1, opener: h})
		unclosedOrders[h.order] = struct{}{}
	}
	unclosed := make([]Issue, 0, len(unclosedOrders))
	for order := range unclosedOrders {
		d := snap.directives[order]
		unclosed = append(unclosed, Issue{
			DirectiveLine:  d.Line,
			Label:          "",
			Code:           IssueUnclosedRange,
			DirectiveOrder: order,
		})
	}

	for l, ps := range idx.points {
		sort.SliceStable(ps, func(i, j int) bool {
			if ps[i].line != ps[j].line {
				return ps[i].line < ps[j].line
			}
			return lessWinner(keyOf(ps[i].hit), keyOf(ps[j].hit))
		})
		idx.points[l] = ps
	}
	for l, rs := range idx.ranges {
		sort.Slice(rs, func(i, j int) bool { return rs[i].start < rs[j].start })
		idx.ranges[l] = rs
	}
	return idx, labelIssues, unclosed
}

// addPoint 登记本行/下一行指令在 target 行上的有效标签命中。
func addPoint(idx *coverageIndex, snap snapshot, d acceptedDirective, target int) {
	for _, l := range d.Labels {
		if !knownLabel(snap, l) {
			continue
		}
		h := hit{line: d.Line, order: d.order, label: l, kind: d.Kind}
		idx.points[l] = append(idx.points[l], pointHit{line: target, hit: h})
	}
}

// appendSortedHit 以胜负键有序插入候选（同一标签的候选数量通常很小）。
func appendSortedHit(hs []hit, h hit) []hit {
	k := keyOf(h)
	i := sort.Search(len(hs), func(i int) bool { return lessWinner(k, keyOf(hs[i])) })
	hs = append(hs, hit{})
	copy(hs[i+1:], hs[i:])
	hs[i] = h
	return hs
}

// lookup 返回某标签在 line 行上的最优命中候选；不存在时 ok=false。
// 三类来源各自至多一次 O(log) 查找，复杂度不随指令总数线性增长。
func (idx *coverageIndex) lookup(label string, line int) (hit, bool) {
	var best hit
	found := false
	consider := func(h hit) {
		if !found || lessWinner(keyOf(h), keyOf(best)) {
			best, found = h, true
		}
	}
	if ps, ok := idx.points[label]; ok {
		i := sort.Search(len(ps), func(i int) bool { return ps[i].line >= line })
		if i < len(ps) && ps[i].line == line {
			consider(ps[i].hit)
		}
	}
	if rs, ok := idx.ranges[label]; ok {
		i := sort.Search(len(rs), func(i int) bool { return rs[i].end > line })
		if i < len(rs) && rs[i].start <= line { // Search 已保证 line < rs[i].end
			consider(rs[i].opener)
		}
	}
	if wf, ok := idx.wholeFile[label]; ok && len(wf) > 0 {
		consider(wf[0])
	}
	return best, found
}

// knownLabel 判定标签是否生效：为已知规则名或特殊标签“全部”。
func knownLabel(snap snapshot, label string) bool {
	if label == AllTag {
		return true
	}
	_, ok := snap.rules[label]
	return ok
}

// isBlank 判定理由是否为空或全由空白字符构成。
func isBlank(reason string) bool {
	for _, r := range reason {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' && r != '\v' && r != '\f' {
			return false
		}
	}
	return true
}
