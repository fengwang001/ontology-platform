package ontology

import "sort"

// naiveWorld 是独立实现的朴素串行世界：单 goroutine、无锁、无并发原语。
// 它逐请求、逐尝试地串行执行，其每一步本身就定义了一种合法的串行顺序。
// 与生产 Store/Engine 刻意不共享任何算法代码，保证对照测试确实由两份独立实现给出。
type naiveWorld struct {
	types  map[string]*LinkType
	nver   map[string]uint64
	nlinks map[string]map[string]Link
	ncount map[string]map[ConstraintKey]int

	// 与生产 Store.readStats 同构的证据计数。
	counterReads int
	scans        int
}

func newNaiveWorld() *naiveWorld {
	return &naiveWorld{
		types:  map[string]*LinkType{},
		nver:   map[string]uint64{},
		nlinks: map[string]map[string]Link{},
		ncount: map[string]map[ConstraintKey]int{},
	}
}

func (w *naiveWorld) registerType(lt LinkType) {
	cp := lt
	w.types[lt.ID] = &cp
	if w.nlinks == nil {
		w.nlinks = map[string]map[string]Link{}
	}
}

func (w *naiveWorld) create(id string) {
	w.nver[id] = 0
	w.nlinks[id] = map[string]Link{}
	w.ncount[id] = map[ConstraintKey]int{}
}

// seedLink 在世界中直接放置一条链接（用于构造初始/插队状态）。
func (w *naiveWorld) seedLink(lk Link) {
	lt := w.types[lk.TypeID]
	if _, ok := w.nlinks[lk.A]; !ok {
		w.create(lk.A)
	}
	if _, ok := w.nlinks[lk.B]; !ok {
		w.create(lk.B)
	}
	if _, exists := w.nlinks[lk.A][lk.Key()]; exists {
		return
	}
	w.nlinks[lk.A][lk.Key()] = lk
	w.nlinks[lk.B][lk.Key()] = lk
	if lt != nil && lt.CardinalityA != nil {
		k := ConstraintKey{TypeID: lk.TypeID, Side: SideA}
		w.ncount[lk.A][k]++
	}
	if lt != nil && lt.CardinalityB != nil {
		k := ConstraintKey{TypeID: lk.TypeID, Side: SideB}
		w.ncount[lk.B][k]++
	}
	// 与 Store.commitRaider 播种路径一致：初始链接使目标版本从 0 变为 1。
	w.nver[lk.A]++
	if lk.B != lk.A {
		w.nver[lk.B]++
	}
}

func (w *naiveWorld) snapshot(target string) []Link {
	w.scans++
	out := make([]Link, 0, len(w.nlinks[target]))
	for _, lk := range w.nlinks[target] {
		out = append(out, lk)
	}
	sortLinks(out)
	return out
}

// naiveCheck 对目标侧约束做一次纯基数判定（只数计数器，不遍历链接）。
func (w *naiveWorld) naiveCheck(target string, ops []Op) ([]ConstraintVerdict, *ConstraintKey) {
	intent := map[string]plannedChange{}
	keySet := map[ConstraintKey]bool{}
	for _, op := range ops {
		lt := w.types[op.TypeID]
		if lt == nil || lt.Constraint(op.Side) == nil {
			continue
		}
		lk := Link{TypeID: op.TypeID}
		if op.Side == SideA {
			lk.A, lk.B = target, op.Other
		} else {
			lk.A, lk.B = op.Other, target
		}
		keySet[ConstraintKey{TypeID: op.TypeID, Side: op.Side}] = true
		if cur, seen := intent[lk.Key()]; seen {
			cur.add = op.Add
			intent[lk.Key()] = cur
		} else {
			intent[lk.Key()] = plannedChange{key: lk.Key(), link: lk, add: op.Add}
		}
	}
	keys := make([]ConstraintKey, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].TypeID != keys[j].TypeID {
			return keys[i].TypeID < keys[j].TypeID
		}
		return keys[i].Side < keys[j].Side
	})

	w.counterReads += len(keys)
	verdicts := make([]ConstraintVerdict, 0, len(keys))
	var bad *ConstraintKey
	for _, key := range keys {
		delta := 0
		for _, ch := range intent {
			if ch.link.TypeID != key.TypeID || ch.link.holder(key.Side) != target {
				continue
			}
			_, exists := w.nlinks[target][ch.key]
			if ch.add && !exists {
				delta++
			}
			if !ch.add && exists {
				delta--
			}
		}
		lt := w.types[key.TypeID]
		max := 0
		if c := lt.Constraint(key.Side); c != nil {
			max = c.Max
		}
		current := w.ncount[target][key]
		satisfied := max <= 0 || current+delta <= max
		verdicts = append(verdicts, ConstraintVerdict{
			Constraint: key, Current: current, Delta: delta, Max: max, Satisfied: satisfied,
		})
		if !satisfied && bad == nil {
			k := key
			bad = &k
		}
	}
	return verdicts, bad
}

// naiveApply 串行应用一次提交（调用前已确认版本匹配且全部基数满足）。
func (w *naiveWorld) naiveApply(target string, ops []Op) {
	// 先按链接合并净效果（同 Store.planOps），再逐个应用；整体净变化只推进一次版本。
	intent := map[string]Link{}
	adds := map[string]bool{}
	for _, op := range ops {
		lk := Link{TypeID: op.TypeID}
		if op.Side == SideA {
			lk.A, lk.B = target, op.Other
		} else {
			lk.A, lk.B = op.Other, target
		}
		intent[lk.Key()] = lk
		adds[lk.Key()] = op.Add
	}

	changed := false
	keys := make([]string, 0, len(intent))
	for k := range intent {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		lk := intent[k]
		lt := w.types[lk.TypeID]
		_, exists := w.nlinks[target][k]
		if adds[k] && !exists {
			w.nlinks[lk.A][k] = lk
			w.nlinks[lk.B][k] = lk
			if lt.CardinalityA != nil {
				w.ncount[lk.A][ConstraintKey{TypeID: lk.TypeID, Side: SideA}]++
			}
			if lt.CardinalityB != nil {
				w.ncount[lk.B][ConstraintKey{TypeID: lk.TypeID, Side: SideB}]++
			}
			changed = true
		}
		if !adds[k] && exists {
			delete(w.nlinks[lk.A], k)
			delete(w.nlinks[lk.B], k)
			if lt.CardinalityA != nil {
				w.ncount[lk.A][ConstraintKey{TypeID: lk.TypeID, Side: SideA}]--
			}
			if lt.CardinalityB != nil {
				w.ncount[lk.B][ConstraintKey{TypeID: lk.TypeID, Side: SideB}]--
			}
			changed = true
		}
	}
	if changed {
		w.nver[target]++
	}
}

// raiderApply 与 Store.commitRaider 同构：以当前版本为基线应用一组变更，
// 基数始终满足（夹具保证），只要产生净变更就推进一次版本。
func (w *naiveWorld) raiderApply(target string, ops []Op) {
	w.naiveApply(target, ops)
}

// naiveRun 在朴素串行世界中执行一个请求的全部内部尝试。
// before(req, attempt, readVersion) 与生产引擎的 SettleHook 同构：
// 它在“重新读取之后、判定之前”串行执行其他请求，从而确定性地模拟插队。
func naiveRun(w *naiveWorld, req Request, maxAttempts int, before SettleHook) *Result {
	res := &Result{}
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		readVersion := w.nver[req.Instance]
		expected := req.Baseline
		if attempt > 1 {
			expected = readVersion
		}
		if before != nil {
			before(req, attempt, readVersion)
		}

		// 记录统一取“判定时点”：钩子之后的最新状态。
		freshVersion := w.nver[req.Instance]
		freshSnapshot := w.snapshot(req.Instance)
		rec := AttemptRecord{Index: attempt, VersionRead: freshVersion, Snapshot: freshSnapshot}

		// 优先级 1：版本冲突（串行世界中直接比较版本，等价于锁内 CAS）。
		if freshVersion != expected {
			verdicts, _ := w.naiveCheck(req.Instance, req.Ops) // 当次最新读取仍完整记录
			rec.Outcome = OutcomeConflict
			rec.Verdicts = copyVerdicts(verdicts)
			res.Attempts = append(res.Attempts, rec)
			if attempt >= maxAttempts {
				res.Reject = &Rejection{
					Code:    CodeRetriesExhausted,
					Message: "optimistic update rejected: retry budget exhausted after repeated version conflicts",
				}
				return res
			}
			// 冲突尝试只命中“版本冲突”一种原因：即使此刻基数已满，也继续重试
			//（名额可能在后续尝试前空出），直至成功/版本匹配后的基数判定/预算耗尽。
			continue
		}

		// 优先级 2：在最新状态上逐约束重新校验。
		verdicts, bad := w.naiveCheck(req.Instance, req.Ops)
		rec.Verdicts = copyVerdicts(verdicts)
		if bad != nil {
			rec.Outcome = OutcomeCardinality
			k := *bad
			res.Attempts = append(res.Attempts, rec)
			res.Reject = &Rejection{
				Code:       CodeCardinality,
				Message:    "cardinality constraint violated after fresh re-check",
				Constraint: &k,
			}
			return res
		}

		// 优先级 3 不适用：版本匹配且基数满足，成功提交。
		w.naiveApply(req.Instance, req.Ops)
		rec.Outcome = OutcomeCommitted
		// 成功记录反映提交后可见状态。
		rec.VersionRead = w.nver[req.Instance]
		rec.Snapshot = w.snapshot(req.Instance)
		res.Attempts = append(res.Attempts, rec)
		res.Committed = true
		res.Version = w.nver[req.Instance]
		return res
	}
	res.Reject = &Rejection{Code: CodeRetriesExhausted, Message: "retry budget exhausted"}
	return res
}
