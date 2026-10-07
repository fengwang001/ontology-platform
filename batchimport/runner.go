package batchimport

import (
	"sort"
	"sync"
)

// preparedRecord 是参数校验阶段的产物。
//
// 参数校验（含字段类型检查）与列表顺序、钩子状态无关，可以用任意内部
// 并发度并行执行；而钩子触发与可见性解析严格串行、按列表顺序进行。
// 两阶段分离保证「同一输入列表以任意内部并发度执行，结果完全一致」。
type preparedRecord struct {
	index int
	rec   Record
	err   error
}

// Batch 以指定整体语义执行一次有序批量导入。
//
// concurrency 仅控制与顺序无关的参数校验阶段的并行度（<=1 视为串行）；
// 钩子触发、批内可见性解析、提交/撤销始终在全局锁内按列表顺序执行。
func (r *Registry) Batch(records []Record, semantics Semantics, concurrency int) *Report {
	if semantics != AllOrNothing && semantics != BestEffort {
		semantics = AllOrNothing
	}
	prepared := r.prepare(records, concurrency)
	r.mu.Lock()
	defer r.mu.Unlock()
	return runBatchLocked(r, records, prepared, semantics)
}

// prepare 并行完成逐条参数合法性校验，并统一检测「同一主键重复出现」。
// 重复检测按首次出现位置之后的第一次命中报错，结果与并发度无关。
func (r *Registry) prepare(records []Record, concurrency int) []preparedRecord {
	out := make([]preparedRecord, len(records))

	if concurrency <= 1 {
		for i, rec := range records {
			out[i] = preparedRecord{index: i, rec: rec, err: r.validateRecord(rec, i)}
		}
	} else {
		jobs := make(chan int)
		var wg sync.WaitGroup
		for w := 0; w < concurrency; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range jobs {
					out[i] = preparedRecord{
						index: i,
						rec:   records[i],
						err:   r.validateRecord(records[i], i),
					}
				}
			}()
		}
		for i := range records {
			jobs <- i
		}
		close(jobs)
		wg.Wait()
	}

	dupErrs := duplicateParams(records)
	for i := range out {
		if out[i].err == nil {
			if reason, ok := dupErrs[i]; ok {
				out[i].err = invalidParam(i, reason)
			}
		}
	}
	return out
}

// duplicateParams 返回每个重复主键所在下标（首次出现位置之后的每一次命中）
// 及其原因。「同一主键重复出现」是输入列表的静态非法属性，
// 与单条记录钩子是否通过无关，故无差别检测；多组重复必须全部报告。
// 键采用 类型+主键 复合形式，不同对象类型互不影响。
func duplicateParams(records []Record) map[int]string {
	seen := make(map[string]int, len(records))
	dups := map[int]string{}
	for i, rec := range records {
		key := rec.Type + "\x00" + rec.ID
		if _, ok := seen[key]; ok {
			dups[i] = "duplicate primary key: " + rec.Type + "/" + rec.ID
		}
		seen[key] = i
	}
	return dups
}

// runBatchLocked 在已持有全局锁的前提下执行有序钩子、应用、提交/撤销。
func runBatchLocked(r *Registry, raw []Record, prepared []preparedRecord, semantics Semantics) *Report {
	results := make([]RecordResult, len(raw))
	for i := range results {
		results[i] = RecordResult{Index: i}
	}

	// 全有或全无：参数非法先于一切钩子处理——任意一条参数非法即整批拒绝，
	// 前置钩子与批次级后置钩子都不会触发。
	if semantics == AllOrNothing {
		var firstErr error
		for _, p := range prepared {
			if p.err != nil {
				results[p.index].Reason = p.err.Error()
				if firstErr == nil {
					firstErr = p.err
				}
			}
		}
		if firstErr != nil {
			return &Report{Semantics: semantics, Committed: false, Records: results, Err: firstErr}
		}
	}

	st := newStaging()
	scratch := map[string]any{}

	// 按列表顺序逐条：校验 → 前置钩子 → 应用进暂存覆盖层。
	for _, p := range prepared {
		if semantics == BestEffort && p.err != nil {
			// 尽力而为：参数非法的记录被跳过，不推进覆盖层上界，
			// 因而不计入后续同批次记录的可见范围。
			results[p.index].Reason = p.err.Error()
			continue
		}

		ctx := &HookContext{
			index:   p.index,
			reg:     r,
			st:      st,
			scratch: scratch,
			visibleIDs: func(typeName string) []string {
				return st.visibleIDs(r, typeName)
			},
		}
		stepStart := ctx.enterHook()
		hookErr := r.runPreHooks(p.rec, ctx)
		results[p.index].VisibilitySteps = ctx.leaveHook(stepStart)
		if hookErr != nil {
			hookErr = normalize(p.index, KindPreHook, hookErr)
			results[p.index].Reason = hookErr.Error()
			if semantics == AllOrNothing {
				// 当前记录尚未写入覆盖层；逆序撤销此前已应用的全部记录。
				st.rollback()
				return &Report{Semantics: semantics, Committed: false, Records: results, Err: hookErr}
			}
			continue
		}

		st.apply(p.rec)
		results[p.index].OK = true
	}

	// 批次级后置钩子：仅全有或全无语义触发一次；此时全部记录都已通过
	// 前置钩子并应用于覆盖层（尚未提交），看到的是整批最终状态。
	if semantics == AllOrNothing {
		postCtx := &PostHookContext{reg: r, st: st, scratch: scratch}
		if err := r.runPostHooks(postCtx); err != nil {
			err = normalize(-1, KindPostHook, err)
			st.rollback() // 整批（含全部单条记录）一起撤销。
			// 后置钩子失败时，逐条结果仍如实标注为「曾通过前置钩子」，
			// 但 Committed=false 且 Err 为批次级错误，表明无一条生效。
			return &Report{Semantics: semantics, Committed: false, Records: results, Err: err}
		}
		st.commit(r)
		return &Report{Semantics: semantics, Committed: true, Records: results}
	}

	// 尽力而为：成功的记录逐条合并（无批次级后置钩子）。
	st.commit(r)
	return &Report{Semantics: semantics, Committed: true, Records: results}
}

// runPreHooks 按注册顺序触发该类型的全部前置钩子；第一个失败即返回。
func (r *Registry) runPreHooks(rec Record, ctx *HookContext) error {
	t := r.types[rec.Type]
	for _, h := range t.PreHooks {
		if err := h(ctx, rec); err != nil {
			return err
		}
	}
	return nil
}

// runPostHooks 触发本批次涉及到的每个对象类型的批次级后置钩子，
// 按类型名字典序触发以保证可重放确定性；第一个失败即返回。
func (r *Registry) runPostHooks(ctx *PostHookContext) error {
	names := make([]string, 0, len(stOverlayTypes(ctx.st)))
	for name := range stOverlayTypes(ctx.st) {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t := r.types[name]
		for _, h := range t.PostHooks {
			if err := h(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

func stOverlayTypes(s *staging) map[string]stagedMap { return s.overlay }
