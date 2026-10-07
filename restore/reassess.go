package restore

import "sort"

// reassess 处理重建中途新发现的损坏。
//
// 固定处理语义：
//  1. 立即停止依赖该备份的后续重建动作——这通过将新损坏点（及其新确认
//     整体不可用的类别下全部记录）置为运行期死点，重算依赖实现；
//  2. 已完成重建的记录不撤销：Progress.Completed 中的记录保持
//     “已完成”，仍可作为后续记录的可用依赖；
//  3. 全部依赖关系重新评估：未完成记录在新快照上重新走逐条核对；
//  4. 运行期产生的阻断一律归为优先级4错误（ErrNewDamage），
//     与初始裁决的前三类错误严格区分。
//
// snap 应为反映新发现损坏后的最新快照（例如对应记录 State 已置为 Corrupt
// 或类别已置为 Missing/CorruptAll）；report 显式声明“新增”了什么，
// 二者必须一致，不一致时以 report 为准做运行期标记。
func reassess(a *Adjudicator, prev *Verdict, snap *Snapshot, prog Progress, report DamageReport) *Verdict {
	_ = a
	_ = prev

	completed := map[RecordID]bool{}
	for id := range prog.Completed {
		completed[id] = true
	}

	runtimeDead := map[RecordID]ReasonCode{}
	for _, id := range report.NewlyCorrupt {
		// 已完成的记录不允许被回溯撤销，忽略对其的损坏报告。
		if !completed[id] {
			runtimeDead[id] = ReasonNewlyCorrupt
		}
	}
	for _, c := range report.NewlyClassUnavailable {
		for _, id := range classIDsOf(snap, c) {
			if !completed[id] {
				runtimeDead[id] = ReasonNewlyCorrupt
			}
		}
	}

	v := runEngine(snap, runtimeDead, completed)

	// 已完成记录即使在新快照中被判损坏，也保持已完成结论。
	for id := range completed {
		v.Records[id] = RecordVerdict{
			ID:          id,
			Recoverable: true,
			Reasons:     []ReasonCode{ReasonAlreadyCompleted},
			Detail:      reasonText(ReasonAlreadyCompleted),
		}
	}

	// 运行期阻断需要把受新损坏级联影响的原因单独标注为
	// ReasonBlockedByNewDamage，以区别于初始裁决阶段的级联。
	if len(runtimeDead) > 0 {
		e := &engine{snap: snap, g: buildGraph(snap), dead: seedDeadSet(snap), memo: map[RecordID]*traverseResult{}}
		affected := e.downstreamOf(runtimeDead, completed)
		for id := range affected {
			if completed[id] {
				continue
			}
			if _, isSeed := runtimeDead[id]; isSeed {
				continue
			}
			rv, ok := v.Records[id]
			if !ok || rv.Recoverable {
				continue
			}
			v.Records[id] = RecordVerdict{
				ID:          id,
				Recoverable: false,
				Reasons:     []ReasonCode{ReasonBlockedByNewDamage},
				Detail:      reasonText(ReasonBlockedByNewDamage),
			}
		}
		recollectRuntimeErrors(v)
	}

	return v
}

// downstreamOf 返回依赖了 seed 中任一记录（含 seed 本身）的全部下游节点，
// 穿过 completed 节点继续传播（已完成节点自身不撤销，但其下游可能受阻）。
func (e *engine) downstreamOf(seed map[RecordID]ReasonCode, completed map[RecordID]bool) map[RecordID]bool {
	reverse := map[RecordID][]RecordID{}
	for from, deps := range e.g.deps {
		for _, to := range deps {
			reverse[to] = append(reverse[to], from)
		}
	}
	seen := map[RecordID]bool{}
	var stack []RecordID
	for id := range seed {
		if !seen[id] {
			seen[id] = true
			stack = append(stack, id)
		}
	}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, next := range reverse[cur] {
			if !seen[next] {
				seen[next] = true
				stack = append(stack, next)
			}
		}
	}
	for id := range completed {
		// 已完成节点自身保持完成；不作为“受阻”集合成员。
		delete(seen, id)
	}
	return seen
}

// seedDeadSet 由快照独立计算初始种子不可用集合（不依赖前次裁决）。
func seedDeadSet(snap *Snapshot) map[RecordID]ReasonCode {
	statuses := classifyClasses(snap)
	dead := map[RecordID]ReasonCode{}
	for _, c := range Classes() {
		if statuses[c] == ClassUnavailable {
			for _, id := range classIDsOf(snap, c) {
				dead[id] = ReasonClassUnavailable
			}
			continue
		}
		for _, id := range classIDsOf(snap, c) {
			if st, ok := recordState(snap, id); ok && st == StateCorrupt {
				dead[id] = ReasonSelfCorrupt
			}
		}
	}
	return dead
}

// classIDsOf 供重评估阶段不构造完整 engine 时枚举类别记录。
func classIDsOf(snap *Snapshot, c Class) []RecordID {
	var keys []string
	seen := map[string]bool{}
	for _, k := range rawKeys(snap, c) {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	ids := make([]RecordID, 0, len(keys))
	for _, k := range keys {
		ids = append(ids, RecordID{Class: c, Key: k})
	}
	return sortRecordIDs(ids)
}

// recollectRuntimeErrors 重排错误列表：初始三类错误保留固定优先级，
// 新损坏相关错误归入优先级4，次序仍按类别/记录键确定。
func recollectRuntimeErrors(v *Verdict) {
	var kept, runtime []VerdictError
	for _, err := range v.Errors {
		if err.Code == ErrNewDamage {
			runtime = append(runtime, err)
			continue
		}
		if err.Record != nil {
			if rv, ok := v.Records[*err.Record]; ok {
				if code, _ := recordErrorCodeStatic(rv); code == ErrNewDamage {
					id := *err.Record
					runtime = append(runtime, VerdictError{
						Code:    ErrNewDamage,
						Class:   &id.Class,
						Record:  &id,
						Message: rv.Detail + ": " + id.String(),
					})
					continue
				}
			}
		}
		kept = append(kept, err)
	}
	v.Errors = append(kept, runtime...)
	sort.SliceStable(v.Errors, func(i, j int) bool {
		if v.Errors[i].Code != v.Errors[j].Code {
			return v.Errors[i].Code < v.Errors[j].Code
		}
		ci, cj := 0, 0
		if v.Errors[i].Class != nil {
			ci = v.Errors[i].Class.Rank()
		}
		if v.Errors[j].Class != nil {
			cj = v.Errors[j].Class.Rank()
		}
		if ci != cj {
			return ci < cj
		}
		ki, kj := "", ""
		if v.Errors[i].Record != nil {
			ki = v.Errors[i].Record.Key
		}
		if v.Errors[j].Record != nil {
			kj = v.Errors[j].Record.Key
		}
		return ki < kj
	})
}

// recordErrorCodeStatic 与 engine.recordErrorCode 的映射保持一致，
// 供不持有 engine 的重评估收尾阶段使用。
func recordErrorCodeStatic(rv RecordVerdict) (ErrorCode, string) {
	has := func(c ReasonCode) bool {
		for _, r := range rv.Reasons {
			if r == c {
				return true
			}
		}
		return false
	}
	switch {
	case has(ReasonNewlyCorrupt), has(ReasonBlockedByNewDamage):
		return ErrNewDamage, rv.Detail + ": " + rv.ID.String()
	case has(ReasonCycle):
		return ErrCycle, rv.Detail + ": " + rv.ID.String()
	default:
		return ErrCascade, rv.Detail + ": " + rv.ID.String()
	}
}
