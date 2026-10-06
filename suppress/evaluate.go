package suppress

import "sort"

// evaluate 是判定主流程：构建覆盖结构、逐条诊断查归属、统计使用、
// 补齐缺理由/无目标行/未知规则/未使用问题，并按规定次序排序输出。
//
// 总开销为 O((D+N) log N) 量级（D 为诊断数、N 为指令的标签出现数），
// 随指令数与诊断数之和近线性；单条诊断的命中判定只对至多两个标签各做
// 常数次 O(log N) 查找，不随指令总数线性增长（见 coverage.lookup）。
func evaluate(snap snapshot) Judgment {
	idx, pairIssues, unclosed := buildCoverage(snap)

	// used 记录“本应生效的（指令，标签）槽位”是否作为归属者抑制过至少一条诊断。
	// 只有归属命中才计使用：未被归属的其他命中者不算使用。
	used := make(map[labelSlot]bool)

	kept := make([]KeptDiagnostic, 0)
	suppressed := make([]SuppressedDiagnostic, 0)

	for diagIndex, diag := range snap.diagnostics {
		// 具体规则标签与“全部”是互不影响的独立标签，各自独立查命中，
		// 再由统一胜负键决定唯一归属。
		specific, ok1 := idx.lookup(diag.Rule, diag.Line)
		any, ok2 := idx.lookup(AllTag, diag.Line)
		winner, ok := specific, ok1
		if ok2 && (!ok || lessWinner(keyOf(any), keyOf(winner))) {
			winner, ok = any, true
		}
		if !ok {
			kept = append(kept, KeptDiagnostic{Diagnostic: diag, Index: diagIndex})
			continue
		}
		used[labelSlot{order: winner.order, label: winner.label}] = true
		suppressed = append(suppressed, SuppressedDiagnostic{
			Diagnostic: diag,
			Attribution: Attribution{
				DirectiveOrder: winner.order,
				DirectiveLine:  winner.line,
				Kind:           winner.kind,
				Label:          winner.label,
			},
			Index: diagIndex,
		})
	}

	issues := collectIssues(snap, pairIssues, used)
	issues = append(issues, unclosed...)

	sortJudgment(&kept, &suppressed, &issues)
	return Judgment{Kept: kept, Suppressed: suppressed, Issues: issues}
}

// collectIssues 汇总全部标签级问题。每个（指令，标签）槽位至多一条问题，
// 按优先级取最高者：缺理由 > 无目标行 > 未知规则 > 重复禁用 > 孤立启用 > 未使用。
func collectIssues(snap snapshot, pairIssues map[labelSlot]IssueCode, used map[labelSlot]bool) []Issue {
	slots := make(map[labelSlot]IssueCode, len(pairIssues))
	for slot, code := range pairIssues {
		slots[slot] = code
	}

	for _, d := range snap.directives {
		missingReason := snap.requireReason && isBlank(d.Reason)
		for _, l := range d.Labels {
			slot := labelSlot{order: d.order, label: l}
			if missingReason {
				// 缺理由使整条指令失效：所有标签都报缺理由，且不产生其他问题。
				putSlot(slots, slot, IssueMissingReason)
				continue
			}
			if !knownLabel(snap, l) {
				// 未知规则标签被忽略；同一指令的其他标签照常处理。
				putSlot(slots, slot, IssueUnknownRule)
				continue
			}
			if d.Kind == KindNextLine && d.Line == snap.totalLines {
				putSlot(slots, slot, IssueNoTargetLine)
				continue
			}
			// 配对阶段问题（重复禁用/孤立启用）若存在则已在 slots 中，
			// 优先级高于未使用；启用指令不报未使用。
			if _, has := slots[slot]; has || d.Kind == KindEnable {
				continue
			}
			// 未使用：作用范围覆盖性候选存在，但没有任何诊断归属于它。
			// 点类/全文件类以其全部覆盖行整体判定；区间类在区间配对成功后，
			// 整个区间内没有归属诊断即报未使用（未闭合的开启者同属此类）。
			if !ineffectiveByKind(snap, d) && !used[slot] {
				slots[slot] = IssueUnused
			}
		}
	}

	issues := make([]Issue, 0, len(slots))
	for slot, code := range slots {
		d := snap.directives[slot.order]
		issues = append(issues, Issue{
			DirectiveLine:  d.Line,
			Label:          slot.label,
			Code:           code,
			DirectiveOrder: slot.order,
		})
	}
	return issues
}

// ineffectiveByKind 判定槽位是否因种类/位置原因根本不产生覆盖，
// 此时不应再报未使用（已有更高优先级问题覆盖，如无目标行）。
func ineffectiveByKind(snap snapshot, d acceptedDirective) bool {
	switch d.Kind {
	case KindNextLine:
		return d.Line >= snap.totalLines
	case KindDisable:
		// 重复禁用不产生区间；其槽位已有 REPEATED_DISABLE，调用方会跳过。
		return false
	default:
		return false
	}
}

// putSlot 按问题优先级写入槽位：只接受比现有类别优先级更高的问题。
func putSlot(slots map[labelSlot]IssueCode, slot labelSlot, code IssueCode) {
	if existing, ok := slots[slot]; !ok || code.priority() < existing.priority() {
		slots[slot] = code
	}
}

// sortJudgment 按输出契约排序三类结果。
func sortJudgment(kept *[]KeptDiagnostic, suppressed *[]SuppressedDiagnostic, issues *[]Issue) {
	sort.SliceStable(*kept, func(i, j int) bool {
		return diagLess((*kept)[i].Diagnostic, (*kept)[i].Index, (*kept)[j].Diagnostic, (*kept)[j].Index)
	})
	sort.SliceStable(*suppressed, func(i, j int) bool {
		return diagLess((*suppressed)[i].Diagnostic, (*suppressed)[i].Index, (*suppressed)[j].Diagnostic, (*suppressed)[j].Index)
	})
	sort.SliceStable(*issues, func(i, j int) bool {
		a, b := (*issues)[i], (*issues)[j]
		if a.DirectiveLine != b.DirectiveLine {
			return a.DirectiveLine < b.DirectiveLine
		}
		// 指令级问题（空标签）排在该指令所有标签级问题之前。
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

// diagLess 为诊断输出次序：（行，列，规则名）升序；
// 完全相同的诊断保持登记相对次序（稳定排序）。
func diagLess(a Diagnostic, ai int, b Diagnostic, bi int) bool {
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	if a.Column != b.Column {
		return a.Column < b.Column
	}
	if a.Rule != b.Rule {
		return a.Rule < b.Rule
	}
	return ai < bi
}
