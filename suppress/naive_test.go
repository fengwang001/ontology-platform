package suppress

import "sort"

// naiveHit 是朴素模型中的一个命中候选：与实现的 hit 完全对应。
type naiveHit struct {
	line  int
	order int
	label string
	kind  Kind
}

func naiveLess(a, b naiveHit) bool {
	sa, sb := 0, 0
	if a.label == AllTag {
		sa = 1
	}
	if b.label == AllTag {
		sb = 1
	}
	ka := [3]int{a.line, a.order, sa}
	kb := [3]int{b.line, b.order, sb}
	for i := range ka {
		if ka[i] != kb[i] {
			return ka[i] < kb[i]
		}
	}
	return false
}

// naiveJudgment 用与生产实现完全独立的“逐行逐标签推演开关状态”方式计算判定：
// 对每个标签、每一行，扫描全部已接受指令，直接推导该标签在该行是否生效，
// 并在所有生效候选中按契约挑选归属者。它故意不共享任何生产代码结构，
// 作为随机对照测试的参照模型（oracle）。
type naiveResult struct {
	kept       []KeptDiagnostic
	suppressed []SuppressedDiagnostic
	issues     []Issue
}

func naiveEvaluate(snap snapshot) naiveResult {
	known := func(l string) bool { return l == AllTag || func() bool { _, ok := snap.rules[l]; return ok }() }
	effective := func(d acceptedDirective) bool {
		return !(snap.requireReason && isBlank(d.Reason))
	}

	// 逐标签、逐行推演禁用开关，并顺带登记点类/全文件候选。
	labels := map[string]struct{}{AllTag: {}}
	for _, d := range snap.directives {
		for _, l := range d.Labels {
			if known(l) {
				labels[l] = struct{}{}
			}
		}
	}

	// cover[label][line] = 该标签在该行的最优候选。
	cover := make(map[string]map[int]naiveHit, len(labels))
	for label := range labels {
		cover[label] = make(map[int]naiveHit)
		for line := 1; line <= snap.totalLines; line++ {
			// 逐指令推演本行开关状态：禁用开启、重复禁用忽略、启用关闭。
			var opener *naiveHit
			for _, d := range sortedByLineOrder(snap.directives) {
				if !effective(d) || !hasLabel(d, label) {
					continue
				}
				switch d.Kind {
				case KindDisable:
					if d.Line <= line && opener == nil {
						cand := naiveHit{line: d.Line, order: d.order, label: label, kind: KindDisable}
						opener = &cand
					}
				case KindEnable:
					if d.Line <= line {
						opener = nil
					}
				}
			}
			if opener != nil {
				cover[label][line] = *opener
			}
		}
		// 点类与全文件。
		for _, d := range snap.directives {
			if !effective(d) || !hasLabel(d, label) {
				continue
			}
			switch d.Kind {
			case KindThisLine:
				putCover(cover, label, d.Line, naiveHit{line: d.Line, order: d.order, label: label, kind: KindThisLine})
			case KindNextLine:
				if d.Line < snap.totalLines {
					putCover(cover, label, d.Line+1, naiveHit{line: d.Line, order: d.order, label: label, kind: KindNextLine})
				}
			case KindWholeFile:
				for line := 1; line <= snap.totalLines; line++ {
					putCover(cover, label, line, naiveHit{line: d.Line, order: d.order, label: label, kind: KindWholeFile})
				}
			}
		}
	}

	used := make(map[labelSlot]bool)
	res := naiveResult{
		kept:       []KeptDiagnostic{},
		suppressed: []SuppressedDiagnostic{},
		issues:     []Issue{},
	}
	for i, diag := range snap.diagnostics {
		var winner naiveHit
		found := false
		if h, ok := cover[diag.Rule][diag.Line]; ok {
			winner, found = h, true
		}
		if h, ok := cover[AllTag][diag.Line]; ok && (!found || naiveLess(h, winner)) {
			winner, found = h, true
		}
		if !found {
			res.kept = append(res.kept, KeptDiagnostic{Diagnostic: diag, Index: i})
			continue
		}
		used[labelSlot{order: winner.order, label: winner.label}] = true
		res.suppressed = append(res.suppressed, SuppressedDiagnostic{
			Diagnostic: diag,
			Attribution: Attribution{
				DirectiveOrder: winner.order,
				DirectiveLine:  winner.line,
				Kind:           winner.kind,
				Label:          winner.label,
			},
			Index: i,
		})
	}

	res.issues = naiveIssues(snap, used)
	sortNaive(&res)
	return res
}

// putCover 以同一行多个候选取最优者的方式写入覆盖。
func putCover(cover map[string]map[int]naiveHit, label string, line int, h naiveHit) {
	cur, ok := cover[label][line]
	if !ok || naiveLess(h, cur) {
		cover[label][line] = h
	}
}

func hasLabel(d acceptedDirective, label string) bool {
	for _, l := range d.Labels {
		if l == label {
			return true
		}
	}
	return false
}

func sortedByLineOrder(ds []acceptedDirective) []acceptedDirective {
	out := append([]acceptedDirective(nil), ds...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].order < out[j].order
	})
	return out
}

// naiveIssues 以独立逻辑推导全部指令问题。
func naiveIssues(snap snapshot, used map[labelSlot]bool) []Issue {
	// 配对：逐标签扫描（行号，登记次序）。
	openSlot := make(map[string]int)
	unclosed := make(map[int]struct{})
	// 标签级问题：逐槽位按优先级只取一条。
	slotCode := make(map[labelSlot]IssueCode)
	choose := func(slot labelSlot, code IssueCode) {
		if ex, ok := slotCode[slot]; !ok || code.priority() < ex.priority() {
			slotCode[slot] = code
		}
	}
	for _, d := range sortedByLineOrder(snap.directives) {
		if snap.requireReason && isBlank(d.Reason) {
			continue
		}
		for _, l := range d.Labels {
			if !knownLabel(snap, l) {
				continue
			}
			switch d.Kind {
			case KindDisable:
				if _, on := openSlot[l]; on {
					choose(labelSlot{order: d.order, label: l}, IssueRepeatedDisable)
				} else {
					openSlot[l] = d.order
				}
			case KindEnable:
				if _, on := openSlot[l]; !on {
					choose(labelSlot{order: d.order, label: l}, IssueOrphanEnable)
				} else {
					delete(openSlot, l)
				}
			}
		}
	}
	for l, order := range openSlot {
		_ = l
		unclosed[order] = struct{}{}
	}
	for _, d := range snap.directives {
		missing := snap.requireReason && isBlank(d.Reason)
		for _, l := range d.Labels {
			slot := labelSlot{order: d.order, label: l}
			switch {
			case missing:
				// 缺理由优先级最高：即使标签未知也报缺理由，整条指令失效。
				choose(slot, IssueMissingReason)
			case !knownLabel(snap, l):
				choose(slot, IssueUnknownRule)
			case d.Kind == KindNextLine && d.Line == snap.totalLines:
				choose(slot, IssueNoTargetLine)
			case d.Kind == KindEnable:
				// 启用指令不报未使用；孤立启用已记录。
			default:
				if !used[slot] {
					choose(slot, IssueUnused)
				}
			}
		}
	}

	out := make([]Issue, 0, len(slotCode)+len(unclosed))
	for slot, code := range slotCode {
		out = append(out, Issue{
			DirectiveLine:  snap.directives[slot.order].Line,
			Label:          slot.label,
			Code:           code,
			DirectiveOrder: slot.order,
		})
	}
	for order := range unclosed {
		out = append(out, Issue{
			DirectiveLine:  snap.directives[order].Line,
			Label:          "",
			Code:           IssueUnclosedRange,
			DirectiveOrder: order,
		})
	}
	return out
}

func sortNaive(r *naiveResult) {
	sort.SliceStable(r.kept, func(i, j int) bool {
		return diagLess(r.kept[i].Diagnostic, r.kept[i].Index, r.kept[j].Diagnostic, r.kept[j].Index)
	})
	sort.SliceStable(r.suppressed, func(i, j int) bool {
		return diagLess(r.suppressed[i].Diagnostic, r.suppressed[i].Index, r.suppressed[j].Diagnostic, r.suppressed[j].Index)
	})
	sort.SliceStable(r.issues, func(i, j int) bool {
		a, b := r.issues[i], r.issues[j]
		if a.DirectiveLine != b.DirectiveLine {
			return a.DirectiveLine < b.DirectiveLine
		}
		if (a.Label == "") != (b.Label == "") {
			return a.Label == ""
		}
		if a.Label != b.Label {
			return a.Label < b.Label
		}
		if a.Code != b.Code {
			return a.Code.priority() < b.Code.priority()
		}
		return a.DirectiveOrder < b.DirectiveOrder
	})
}
