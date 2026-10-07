package restore

import "sort"

// engine 是单次裁决内部使用的可变工作区；它不逃逸到快照，
// 因此多次并发裁决各自拥有独立 engine，互不影响。
type engine struct {
	snap     *Snapshot
	g        *depGraph
	statuses map[Class]ClassStatus
	dead     map[RecordID]ReasonCode
	verdict  *Verdict
	// runtimeDead 非 nil 时，这些原因覆盖普通 dead（重评估阶段使用）。
	runtimeDead map[RecordID]ReasonCode
	// completed 非 nil 时，已完成记录保持“已完成”，不参与重新阻断。
	completed map[RecordID]bool
	memo      map[RecordID]*traverseResult
}

// adjudicate 是一次性裁决的主流程：
// 先做单类内部可用性判定，再做跨类别依赖核对与唯一排序。
//
// 错误固定优先级（高到低）：
//  1. 某类备份整体缺失/整体损坏（ErrClassUnavailable）；
//  2. 部分不可用导致的级联不可重建（ErrCascade）；
//  3. 循环依赖导致重建顺序无法确定（ErrCycle）；
//  4. 重建中途新发现的损坏（ErrNewDamage，仅重评估阶段产生）。
//
// 理由：整体不可用使该类一切结论失去依据，必须最先裁决；级联不可重建
// 在结构层面可独立判定，先于排序问题；环是排序问题，只有在可恢复子图
// 仍存在时才有意义；新损坏属于运行期事件，只可能叠加在已得结论之上。
func adjudicate(a *Adjudicator, snap *Snapshot) *Verdict {
	_ = a
	return runEngine(snap, nil, nil)
}

// runEngine 执行一次完整裁决。runtimeDead/completed 仅重评估时非 nil。
func runEngine(snap *Snapshot, runtimeDead map[RecordID]ReasonCode, completed map[RecordID]bool) *Verdict {
	e := &engine{
		snap:        snap,
		g:           buildGraph(snap),
		statuses:    classifyClasses(snap),
		dead:        map[RecordID]ReasonCode{},
		runtimeDead: runtimeDead,
		completed:   completed,
		memo:        map[RecordID]*traverseResult{},
		verdict: &Verdict{
			ClassStatuses: map[Class]ClassStatus{},
			Records:       map[RecordID]RecordVerdict{},
		},
	}
	e.seedDead()
	e.resolveRecords()
	e.detectAndMarkCycles()
	e.buildPlan()
	e.collectErrors()
	return e.verdict
}

// seedDead 依据单类内部判定生成“种子不可用集合”。
func (e *engine) seedDead() {
	for _, c := range Classes() {
		st := e.statuses[c]
		e.verdict.ClassStatuses[c] = st
		e.auditClass(c, st)
		if st == ClassUnavailable {
			for _, id := range e.classIDs(c) {
				e.dead[id] = ReasonClassUnavailable
			}
			continue
		}
		for _, id := range e.classIDs(c) {
			st, ok := recordState(e.snap, id)
			if ok && st == StateCorrupt {
				e.dead[id] = ReasonSelfCorrupt
			}
		}
	}
}

// resolveRecords 对全部已知记录逐条按依赖链核对可恢复性。
func (e *engine) resolveRecords() {
	for _, id := range allRecordIDs(e.snap) {
		r := e.resolveOne(id)
		e.verdict.Records[id] = r
		e.auditRecord(r)
	}
}

// resolveOne 是单条记录的判定入口，先应用运行期覆盖，再走记忆化判定。
func (e *engine) resolveOne(id RecordID) RecordVerdict {
	if e.completed[id] {
		return RecordVerdict{
			ID:          id,
			Recoverable: true,
			Reasons:     []ReasonCode{ReasonAlreadyCompleted},
			Detail:      reasonText(ReasonAlreadyCompleted),
		}
	}
	if code, ok := e.runtimeDead[id]; ok {
		return RecordVerdict{
			ID:          id,
			Recoverable: false,
			Reasons:     []ReasonCode{code},
			Detail:      reasonText(code),
		}
	}
	r, _ := e.g.resolveWithMemo(id, e.dead, e.memo, nil)
	rv := RecordVerdict{
		ID:          id,
		Recoverable: r.ok,
		Reasons:     []ReasonCode{r.code},
		Detail:      reasonText(r.code),
	}
	return rv
}

// detectAndMarkCycles 在“已知死点之外的活节点”子图上检测环，
// 并把循环成员及其下游从可重建集合剔除。
func (e *engine) detectAndMarkCycles() {
	live := map[RecordID]bool{}
	for id, rv := range e.verdict.Records {
		if rv.Recoverable {
			live[id] = true
		}
	}
	cyclic := e.g.detectCycles(live)
	if len(cyclic) == 0 {
		cyclic = map[RecordID]bool{}
	}
	for _, id := range sortRecordIDs(mapKeys(cyclic)) {
		if !live[id] {
			continue
		}
		e.verdict.Records[id] = RecordVerdict{
			ID:          id,
			Recoverable: false,
			Reasons:     []ReasonCode{ReasonCycle},
			Detail:      reasonText(ReasonCycle),
		}
		e.verdict.Audit = append(e.verdict.Audit, AuditEvent{
			Stage:    "record",
			Class:    id.Class.String(),
			Record:   id.String(),
			Outcome:  "unrecoverable",
			Basis:    reasonText(ReasonCycle),
			Priority: int(ErrCycle),
		})
	}
}

// buildPlan 在最终可恢复集合上生成唯一确定的重建计划。
func (e *engine) buildPlan() {
	recoverable := map[RecordID]bool{}
	for id, rv := range e.verdict.Records {
		if rv.Recoverable {
			recoverable[id] = true
		}
	}
	e.verdict.Plan = e.g.topoPlan(recoverable)
}

// collectErrors 汇总错误并按固定优先级与确定性次序排序。
func (e *engine) collectErrors() {
	var errs []VerdictError

	// 优先级1：类整体不可用。
	for _, c := range Classes() {
		if e.statuses[c] == ClassUnavailable {
			cc := c
			errs = append(errs, VerdictError{
				Code:    ErrClassUnavailable,
				Class:   &cc,
				Message: e.classUnavailableMessage(c),
			})
		}
	}

	// 优先级2与3：逐条记录结论；同一记录若同时有多重原因，
	// 原因按优先级体现在 Reasons 中，这里只登记其最高有效错误码。
	for _, id := range sortRecordIDs(e.recordKeysSorted()) {
		rv := e.verdict.Records[id]
		if rv.Recoverable {
			continue
		}
		code, msg := e.recordErrorCode(rv)
		if code == 0 {
			continue
		}
		rid := id
		errs = append(errs, VerdictError{
			Code:    code,
			Class:   &rid.Class,
			Record:  &rid,
			Message: msg,
		})
	}

	sort.SliceStable(errs, func(i, j int) bool {
		if errs[i].Code != errs[j].Code {
			return errs[i].Code < errs[j].Code
		}
		ci, cj := 0, 0
		if errs[i].Class != nil {
			ci = errs[i].Class.Rank()
		}
		if errs[j].Class != nil {
			cj = errs[j].Class.Rank()
		}
		if ci != cj {
			return ci < cj
		}
		ki, kj := "", ""
		if errs[i].Record != nil {
			ki = errs[i].Record.Key
		}
		if errs[j].Record != nil {
			kj = errs[j].Record.Key
		}
		return ki < kj
	})
	e.verdict.Errors = errs
}

// recordErrorCode 将单条结论映射为错误码；运行期损坏优先于环，环优先于级联。
func (e *engine) recordErrorCode(rv RecordVerdict) (ErrorCode, string) {
	has := func(c ReasonCode) bool {
		for _, r := range rv.Reasons {
			if r == c {
				return true
			}
		}
		return false
	}
	switch {
	case has(ReasonNewlyCorrupt):
		return ErrNewDamage, reasonText(ReasonNewlyCorrupt) + ": " + rv.ID.String()
	case has(ReasonBlockedByNewDamage):
		return ErrNewDamage, reasonText(ReasonBlockedByNewDamage) + ": " + rv.ID.String()
	case has(ReasonCycle):
		return ErrCycle, reasonText(ReasonCycle) + ": " + rv.ID.String()
	case has(ReasonClassUnavailable):
		return ErrCascade, reasonText(ReasonClassUnavailable) + " 级联: " + rv.ID.String()
	case has(ReasonSelfCorrupt):
		return ErrCascade, reasonText(ReasonSelfCorrupt) + ": " + rv.ID.String()
	case has(ReasonDependencyUnavailable), has(ReasonReferenceBroken):
		return ErrCascade, rv.Detail + ": " + rv.ID.String()
	default:
		return 0, ""
	}
}

// classIDs 确定性返回某类备份切片中出现的全部记录标识。
func (e *engine) classIDs(c Class) []RecordID {
	ids := make([]RecordID, 0)
	seen := map[string]bool{}
	for _, k := range rawKeys(e.snap, c) {
		if !seen[k] {
			seen[k] = true
			ids = append(ids, RecordID{Class: c, Key: k})
		}
	}
	return sortRecordIDs(ids)
}

func (e *engine) classUnavailableMessage(c Class) string {
	cb := e.snap.Classes[c]
	if cb.Missing {
		return "类备份整体缺失: " + c.String()
	}
	return "类备份整体损坏: " + c.String()
}

func (e *engine) recordKeysSorted() []RecordID {
	ids := make([]RecordID, 0, len(e.verdict.Records))
	for id := range e.verdict.Records {
		ids = append(ids, id)
	}
	return sortRecordIDs(ids)
}

func (e *engine) auditClass(c Class, st ClassStatus) {
	outcome := "ok"
	switch st {
	case ClassPartial:
		outcome = "partial"
	case ClassUnavailable:
		outcome = "unavailable"
	}
	e.verdict.Audit = append(e.verdict.Audit, AuditEvent{
		Stage:   "class",
		Class:   c.String(),
		Outcome: outcome,
	})
}

func (e *engine) auditRecord(rv RecordVerdict) {
	outcome := "recoverable"
	if !rv.Recoverable {
		outcome = "unrecoverable"
	}
	basis := rv.Detail
	priority := 0
	if !rv.Recoverable {
		switch rv.Reasons[0] {
		case ReasonClassUnavailable, ReasonSelfCorrupt, ReasonDependencyUnavailable, ReasonReferenceBroken:
			if rv.Reasons[0] == ReasonClassUnavailable {
				priority = int(ErrCascade)
			} else {
				priority = int(ErrCascade)
			}
		case ReasonCycle:
			priority = int(ErrCycle)
		case ReasonNewlyCorrupt, ReasonBlockedByNewDamage:
			priority = int(ErrNewDamage)
		}
	}
	e.verdict.Audit = append(e.verdict.Audit, AuditEvent{
		Stage:    "record",
		Class:    rv.ID.Class.String(),
		Record:   rv.ID.String(),
		Outcome:  outcome,
		Basis:    basis,
		Priority: priority,
	})
}

func reasonText(c ReasonCode) string {
	switch c {
	case ReasonIntact:
		return "自身完好且全部依赖可恢复"
	case ReasonClassUnavailable:
		return "所属类备份整体不可用"
	case ReasonSelfCorrupt:
		return "自身记录损坏"
	case ReasonDependencyUnavailable:
		return "依赖项不可恢复导致级联不可重建"
	case ReasonReferenceBroken:
		return "依赖引用指向不存在的记录"
	case ReasonCycle:
		return "处于循环依赖或其下游，重建顺序无法确定"
	case ReasonNewlyCorrupt:
		return "重建过程中新发现损坏"
	case ReasonBlockedByNewDamage:
		return "被新发现损坏级联阻断"
	case ReasonAlreadyCompleted:
		return "新发现损坏前已完成重建，结论不撤销"
	default:
		return "无判定"
	}
}

func mapKeys(m map[RecordID]bool) []RecordID {
	ids := make([]RecordID, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	return ids
}
