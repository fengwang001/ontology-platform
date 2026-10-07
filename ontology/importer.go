package ontology

import "sync"

// RecordStatus 是单条记录在结果清单中的判定。
type RecordStatus string

const (
	// StatusApplied 记录最终生效。
	StatusApplied RecordStatus = "APPLIED"
	// StatusFailed 记录被拒绝（尽力而为：跳过；全有全无：整批回滚）。
	StatusFailed RecordStatus = "FAILED"
	// StatusSkippedUnprocessed 全有全无语义下，因更早记录失败
	// 而根本未触发前置钩子、未被处理的后续记录。
	StatusSkippedUnprocessed RecordStatus = "SKIPPED_UNPROCESSED"
)

// RecordResult 是一条记录的结果，清单顺序与输入列表顺序严格一致。
type RecordResult struct {
	Index  int
	PK     string
	Status RecordStatus
	Err    *HookError // 失败原因；成功为 nil
	// Access 为该记录钩子解析可见性时的访问度量（复杂度证据）。
	Access AccessCounters
}

// BatchResult 是整批导入结果。
type BatchResult struct {
	Type      string
	Semantic  Semantics
	Records   []RecordResult
	PostError *HookError // 后置钩子失败原因（仅全有全无可能出现）
	Committed bool
	// Retries 为乐观冲突导致的整批重放次数（跨批次并发验证用）。
	Retries int
}

// FirstError 按「参数非法 -> 前置钩子 -> 后置钩子」的固定次序
// 返回整批第一个命中的原因；全部成功返回 nil。
func (b *BatchResult) FirstError() *HookError {
	var firstParam *HookError
	var firstPre *HookError
	for i := range b.Records {
		e := b.Records[i].Err
		if e == nil {
			continue
		}
		switch e.Kind {
		case KindParamInvalid:
			if firstParam == nil {
				firstParam = e
			}
		case KindPreHook:
			if firstPre == nil {
				firstPre = e
			}
		}
	}
	if firstParam != nil {
		return firstParam
	}
	if firstPre != nil {
		return firstPre
	}
	return b.PostError
}

// Importer 是批量导入执行器，编排三个协作角色：
//   - 自身（分批执行模块）：取快照、有序处理、提交/撤销/重放；
//   - overlay + ScopedView（钩子触发与批内可见性解析模块）；
//   - HookError 归一化（错误归一化模块），经 FirstError 统一排序。
type Importer struct {
	reg *Registry

	// workers 为「内部并发度」。语义上记录按列表顺序逐条解析，
	// 该参数仅控制可并行阶段（参数校验）与调度抖动，
	// 结果不随其取值变化（1..N 完全一致）。
	workers int
	// jitterHook 非空时在每条记录处理前后被调用，
	// 用于测试中注入随机延迟、放大调度不确定性。
	jitterHook func(index int)
}

// NewImporter 创建执行器。workers<=0 时取 1。
func NewImporter(reg *Registry, workers int) *Importer {
	if workers < 1 {
		workers = 1
	}
	return &Importer{reg: reg, workers: workers}
}

// WithJitter 注入调度抖动钩子（测试用），返回自身。
func (im *Importer) WithJitter(f func(index int)) *Importer {
	im.jitterHook = f
	return im
}

// Batch 描述一次导入请求。
type Batch struct {
	Type     string
	Semantic Semantics
	Records  []Record
}

// Import 执行一次批量导入。
// 跨批次并发下采用「快照读 + 版本检测 + 冲突整批重放」，
// 最终可线性化且与某个全局串行顺序等价；重放同一批次结果一致。
func (im *Importer) Import(st *Store, b Batch) *BatchResult {
	ot, ok := im.reg.Lookup(b.Type)
	if !ok {
		return &BatchResult{
			Type: b.Type, Semantic: b.Semantic,
			PostError: postError("unknown_type", "object type not registered"),
		}
	}

	var retries int
	for {
		snap := st.Snapshot()
		res := im.runOnce(st, ot, snap, b)
		if !res.Committed && res.PostError != nil && res.PostError.Code == "commit_conflict" {
			retries++
			continue
		}
		res.Retries = retries
		return res
	}
}

// runOnce 在给定快照上确定性地跑完整个有序流程。
func (im *Importer) runOnce(st *Store, ot *ObjectType, snap *Snapshot, b Batch) *BatchResult {
	n := len(b.Records)
	results := make([]RecordResult, n)

	// 阶段 0：参数非法之主键重复（整批结构性检查，最先报告）。
	seen := make(map[string]int, n)
	for i, rec := range b.Records {
		if first, dup := seen[rec.PK]; dup {
			he := paramError(i, "duplicate_pk",
				"primary key repeated within batch (first at index "+itoa(first)+")")
			for j := range results {
				results[j] = RecordResult{Index: j, PK: b.Records[j].PK,
					Status: StatusFailed, Err: he}
			}
			results[i].Err = he
			return &BatchResult{Type: b.Type, Semantic: b.Semantic,
				Records: results, Committed: false}
		}
		seen[rec.PK] = i
		results[i] = RecordResult{Index: i, PK: rec.PK, Status: StatusFailed}
	}

	ov := newOverlay(b.Type, snap, ot.Aggregates)

	// 阶段 1：字段类型校验。可按任意内部并发度并行（无共享可变状态）。
	typeErrs := im.validateParallel(ot, b.Records)

	// 全有全无语义下，参数非法（类型不符）优先于前置钩子：
	// 若存在任意类型错误，取列表中首个，整批拒绝，不再触发任何前置钩子。
	if b.Semantic == SemAllOrNothing {
		for i, he := range typeErrs {
			if he != nil {
				e := *he
				e.Index = i
				rejectAll(results, &e)
				return &BatchResult{Type: b.Type, Semantic: b.Semantic,
					Records: results, Committed: false}
			}
		}
	}

	// 阶段 2：按列表顺序逐条「前置钩子 -> apply」。
	// 这是顺序语义的核心：第 i 条的 view 只能看到 [0,i) 中已通过者，
	// 因此第 i 步 happens-before 第 i+1 步，不随并发度改变。
	var firstFailure int = -1
	for i := 0; i < n; i++ {
		if im.jitterHook != nil {
			im.jitterHook(i)
		}
		rec := b.Records[i]
		r := &results[i]

		if typeErrs[i] != nil {
			e := *typeErrs[i]
			e.Index = i
			r.Err = &e
			r.Status = StatusFailed
			continue // 尽力而为：跳过，且不 apply -> 不进入后续可见范围
		}

		counters := &AccessCounters{}
		ctx := &HookContext{TypeName: b.Type, Index: i}
		view := ov.view(counters)
		if he := ot.Pre(ctx, rec, view); he != nil {
			e := *he
			e.Kind = KindPreHook
			e.Index = i
			r.Err = &e
			r.Status = StatusFailed
			r.Access = *counters
			if b.Semantic == SemAllOrNothing {
				firstFailure = i
				break
			}
			continue
		}
		r.Access = *counters

		ov.apply(rec) // 只有通过前置钩子的记录才影响可见范围
		r.Status = StatusApplied
	}

	if b.Semantic == SemBestEffort {
		// 尽力而为：已应用的全部提交；后置钩子不触发。
		changed := ov.finalChanged()
		if !st.Commit(snap, b.Type, changed) {
			return conflictResult(b, results)
		}
		return &BatchResult{Type: b.Type, Semantic: b.Semantic,
			Records: results, Committed: true}
	}

	// 全有或全无
	if firstFailure >= 0 {
		// 撤销已应用记录（逆序），overlay 回到批次开始前；无提交。
		ov.UndoAll()
		// 排在 firstFailure 之后、从未处理的记录标记为「未处理」，
		// 不套用仅用于整批回滚的 batch_rolled_back。
		for j := firstFailure + 1; j < n; j++ {
			if results[j].Err == nil && results[j].Status == StatusFailed {
				results[j].Status = StatusSkippedUnprocessed
			}
		}
		return &BatchResult{Type: b.Type, Semantic: b.Semantic,
			Records: markRolledBack(results), Committed: false}
	}

	// 全部前置通过：提交前触发一次批次级后置钩子，
	// 看到的是全部记录都已应用、尚未提交的最终状态。
	if ot.Post != nil {
		postView := ov.view(nil)
		if he := ot.Post(&HookContext{TypeName: b.Type, Index: -1}, postView); he != nil {
			e := *he
			e.Kind = KindPostHook
			e.Index = -1
			ov.UndoAll() // 后置失败必须整批一起撤销
			return &BatchResult{Type: b.Type, Semantic: b.Semantic,
				Records: markRolledBack(results), PostError: &e, Committed: false}
		}
	}

	changed := ov.finalChanged()
	if !st.Commit(snap, b.Type, changed) {
		return conflictResult(b, results)
	}
	return &BatchResult{Type: b.Type, Semantic: b.Semantic,
		Records: results, Committed: true}
}

func conflictResult(b Batch, results []RecordResult) *BatchResult {
	return &BatchResult{
		Type: b.Type, Semantic: b.Semantic, Records: results,
		PostError: postError("commit_conflict", "optimistic conflict; batch replayed"),
		Committed: false,
	}
}

func markRolledBack(results []RecordResult) []RecordResult {
	for i := range results {
		if results[i].Status == StatusApplied {
			results[i].Status = StatusFailed
			results[i].Err = &HookError{
				Kind: KindPostHook, Index: -1,
				Code:    "batch_rolled_back",
				Message: "record reverted because the batch did not commit",
			}
		}
	}
	return results
}

// rejectAll 在整批处理前因结构性参数非法被拒时，
// 把全部记录统一标记为失败并携带同一首个原因。
func rejectAll(results []RecordResult, he *HookError) {
	for j := range results {
		results[j].Status = StatusFailed
		results[j].Err = he
	}
}

// validateParallel 以可配置的内部并发度执行无共享的字段类型校验。
// 无论并发度多少，每个下标的判定完全确定。
func (im *Importer) validateParallel(ot *ObjectType, recs []Record) []*HookError {
	errs := make([]*HookError, len(recs))
	if im.workers <= 1 || len(recs) <= 1 {
		for i := range recs {
			errs[i] = ot.validateSchema(recs[i])
		}
		return errs
	}
	sem := make(chan struct{}, im.workers)
	var wg sync.WaitGroup
	for i := range recs {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			errs[i] = ot.validateSchema(recs[i])
		}(i)
	}
	wg.Wait()
	return errs
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	p := len(buf)
	for i > 0 {
		p--
		buf[p] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		p--
		buf[p] = '-'
	}
	return string(buf[p:])
}
