package ontology

import (
	"container/heap"
	"fmt"
	"sort"
	"strings"
)

// EntryKind 区分条目声明的是对象实例还是链接实例。
type EntryKind int

const (
	EntryObject EntryKind = iota
	EntryLink
)

// Mode 是导入请求的失败处理模式。
type Mode int

const (
	// BestEffort 模式：任何条目失败都不影响其余条目的最终落地结果。
	// 每个条目在独立的临界区内落地，并发读者可能观察到中间态
	// （隔离边界 = 单条目落地，详见 DESIGN.md）。
	BestEffort Mode = iota
	// Atomic 模式：任意条目失败则回滚全部已落地条目。
	// 整个导入持有存储写锁，对外只呈现导入前或导入后的状态。
	Atomic
)

// Verdict 是条目最终判定结果，五类两两可区分。
type Verdict int

const (
	// VerdictSucceeded 成功落地（Atomic 回滚中逆操作失败时也保持该状态，
	// 此时条目会同时出现在 Report.RollbackFailures 中）。
	VerdictSucceeded Verdict = iota
	// VerdictValidationFailed 自身违反对象类型或属性校验规则。
	VerdictValidationFailed
	// VerdictPropagatedFailed 因引用的同批次条目失败而失败，未曾尝试落地。
	VerdictPropagatedFailed
	// VerdictCardinalityConflict 因基数冲突未被放行。
	VerdictCardinalityConflict
	// VerdictRolledBack 曾成功落地，因整体回滚而撤销。
	VerdictRolledBack
)

func (v Verdict) String() string {
	switch v {
	case VerdictSucceeded:
		return "SUCCEEDED"
	case VerdictValidationFailed:
		return "VALIDATION_FAILED"
	case VerdictPropagatedFailed:
		return "PROPAGATED_FAILED"
	case VerdictCardinalityConflict:
		return "CARDINALITY_CONFLICT"
	case VerdictRolledBack:
		return "ROLLED_BACK"
	}
	return "UNKNOWN"
}

// Entry 声明创建一个对象实例或一条链接实例。
type Entry struct {
	ID   string
	Kind EntryKind

	// ---- 对象条目字段 ----
	ObjectTypeRID string
	// RID 可选；为空时分配确定性 RID "obj-<EntryID>"。
	RID string
	// Properties 的值可以是 string / int，也可以是 Ref（对象引用属性）。
	Properties map[string]any

	// ---- 链接条目字段 ----
	LinkTypeRID string
	Source      Ref
	Target      Ref
}

// EntryResult 是单个条目的最终判定。
type EntryResult struct {
	EntryID string
	Verdict Verdict
	// Reason 记录判定依据（失败原因 / 放行依据），保证可追溯。
	Reason string
	// ObjectRID / LinkRID 是落地时实际分配的实例 RID。
	ObjectRID string
	LinkRID   string
}

// RollbackFailure 记录回滚期间逆操作失败的条目。
type RollbackFailure struct {
	EntryID string
	RID     string
	Err     string
}

// Report 是一次导入请求的完整判定报告。
type Report struct {
	// Rejected 为 true 表示请求在落地前被整体拒绝（如引用环、条目 ID 重复），
	// 此时不产生任何可观察的对象图改动，Results 为空。
	Rejected     bool
	RejectReason string
	// Results 按条目声明顺序给出每个条目的最终判定。
	Results []EntryResult
	// RolledBack 表示 Atomic 模式下发生了整体回滚。
	RolledBack bool
	// RollbackFailures 汇总回滚期间逆操作失败的条目；这些条目保留
	// VerdictSucceeded 判定（其实例仍存在于图中），与此处记录共同保证可追溯。
	RollbackFailures []RollbackFailure
}

// Result 按条目 ID 查询判定结果。
func (r Report) Result(entryID string) (EntryResult, bool) {
	for _, res := range r.Results {
		if res.EntryID == entryID {
			return res, true
		}
	}
	return EntryResult{}, false
}

// LogRecord 是每个条目的审计日志：输入、判定结果与依据。
type LogRecord struct {
	Index  int // 声明顺序下标
	Entry  Entry
	Result EntryResult
}

// Request 是一次批量导入请求。
type Request struct {
	Entries []Entry
	Mode    Mode
}

// Importer 执行批量导入。
type Importer struct {
	store *Store
	logf  func(LogRecord)
}

// NewImporter 构造导入器；logf 非nil时对每个条目输出审计日志（输入、判定、依据）。
func NewImporter(s *Store, logf func(LogRecord)) *Importer {
	return &Importer{store: s, logf: logf}
}

// landedOp 记录一次成功落地，供 Atomic 模式回滚使用。
type landedOp struct {
	entryID string
	rid     string
	kind    EntryKind
}

// importCtx 是一次导入的内部状态。
type importCtx struct {
	req       Request
	entryByID map[string]*Entry
	declIndex map[string]int
	results   map[string]*EntryResult
	// nonCandidate 标记批次内因声明顺序靠后而不被放行的链接条目。
	nonCandidate map[string]string // entryID -> 冲突原因
	landed       []landedOp
	pendingHooks []string
	// rollbackFailures 汇总回滚期间逆操作失败的条目。
	rollbackFailures []RollbackFailure
}

// assignedObjectRID 给出对象条目落地时将使用的确定性 RID。
func assignedObjectRID(e *Entry) string {
	if e.RID != "" {
		return e.RID
	}
	return "obj-" + e.ID
}

// Import 执行一次批量导入，返回完整判定报告。
func (imp *Importer) Import(req Request) Report {
	s := imp.store

	// 1. 条目 ID 唯一性检查；重复则无法消歧引用，整体拒绝。
	entryByID := make(map[string]*Entry, len(req.Entries))
	declIndex := make(map[string]int, len(req.Entries))
	for i := range req.Entries {
		e := &req.Entries[i]
		if e.ID == "" {
			return Report{Rejected: true, RejectReason: fmt.Sprintf("第 %d 个条目 ID 为空", i)}
		}
		if _, dup := entryByID[e.ID]; dup {
			return Report{Rejected: true, RejectReason: "条目 ID 重复: " + e.ID}
		}
		entryByID[e.ID] = e
		declIndex[e.ID] = i
	}

	// 2. 构建引用图并做拓扑排序（Kahn，O(V+E)，基于哈希邻接，不做两两比较）。
	deps := buildDeps(req.Entries, entryByID)
	order, cyclic := topoSort(req.Entries, deps)
	if len(cyclic) > 0 {
		return Report{
			Rejected:     true,
			RejectReason: "条目引用关系构成环，整体拒绝: " + strings.Join(cyclic, ", "),
		}
	}

	ctx := &importCtx{
		req:          req,
		entryByID:    entryByID,
		declIndex:    declIndex,
		results:      make(map[string]*EntryResult, len(req.Entries)),
		nonCandidate: markNonCandidates(req.Entries, entryByID, s),
	}

	hooks := s.hooks
	fireHooks := func() {
		if hooks.AfterLand == nil {
			ctx.pendingHooks = ctx.pendingHooks[:0]
			return
		}
		for _, rid := range ctx.pendingHooks {
			hooks.AfterLand(rid)
		}
		ctx.pendingHooks = ctx.pendingHooks[:0]
	}

	// 3. 按拓扑序处理条目。Atomic 模式全程持写锁（隔离边界 = 整个导入）；
	//    BestEffort 模式每条目一个临界区。
	if req.Mode == Atomic {
		s.mu.Lock()
		for _, id := range order {
			imp.processOne(ctx, id)
			fireHooks()
		}
		imp.rollbackIfNeeded(ctx)
		s.mu.Unlock()
	} else {
		for _, id := range order {
			s.mu.Lock()
			imp.processOne(ctx, id)
			s.mu.Unlock()
			fireHooks()
		}
	}

	// 4. 按声明顺序汇总报告并输出审计日志。
	return imp.assemble(ctx)
}

// assemble 汇总最终结果，构造报告并逐条输出日志。
func (imp *Importer) assemble(ctx *importCtx) Report {
	rep := Report{}
	anyFailure := false
	for i := range ctx.req.Entries {
		e := &ctx.req.Entries[i]
		res := ctx.results[e.ID]
		if res.Verdict != VerdictSucceeded && res.Verdict != VerdictRolledBack {
			anyFailure = true
		}
		rep.Results = append(rep.Results, *res)
		if imp.logf != nil {
			imp.logf(LogRecord{Index: i, Entry: *e, Result: *res})
		}
	}
	if ctx.req.Mode == Atomic && anyFailure {
		rep.RolledBack = true
	}
	rep.RollbackFailures = ctx.rollbackFailures
	return rep
}

// entryDeps 提取单个条目引用的同批次条目 ID（已去重）。
func entryDeps(e *Entry, entryByID map[string]*Entry) []string {
	seen := map[string]bool{}
	var out []string
	add := func(ref Ref) {
		if ref.EntryID == "" {
			return
		}
		if _, ok := entryByID[ref.EntryID]; !ok {
			return // 未知条目引用不建边，留待校验阶段报自身校验失败
		}
		if !seen[ref.EntryID] {
			seen[ref.EntryID] = true
			out = append(out, ref.EntryID)
		}
	}
	if e.Kind == EntryObject {
		for _, v := range e.Properties {
			if ref, ok := v.(Ref); ok {
				add(ref)
			}
		}
	} else {
		add(e.Source)
		add(e.Target)
	}
	return out
}

// buildDeps 构建引用图：deps[id] 是 id 落地前必须先处理的同批次条目。
func buildDeps(entries []Entry, entryByID map[string]*Entry) map[string][]string {
	deps := make(map[string][]string, len(entries))
	for i := range entries {
		deps[entries[i].ID] = entryDeps(&entries[i], entryByID)
	}
	return deps
}

// readyHeap 是按声明索引排序的最小堆，使拓扑排序的就绪条目选择确定性。
type readyHeap []int

func (h readyHeap) Len() int           { return len(h) }
func (h readyHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h readyHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *readyHeap) Push(x any)        { *h = append(*h, x.(int)) }
func (h *readyHeap) Pop() any {
	old := *h
	n := len(old)
	v := old[n-1]
	*h = old[:n-1]
	return v
}

// topoSort 使用 Kahn 算法排序：每个条目的引用只被枚举一次，邻接关系存于
// 哈希表，不对条目做两两比较。就绪条目通过最小堆按声明索引选择，使落地
// 顺序成为规范顺序（拓扑序 + 声明顺序打破平局），与落地先后相关的判定
// （RID 占用、联合基数）因此确定。复杂度 O((V+E) log V)，非平方。
// 返回排序后的条目 ID 序列；若存在环，cyclic 非空（环上条目 ID，排序后返回）。
func topoSort(entries []Entry, deps map[string][]string) (order []string, cyclic []string) {
	indegree := make(map[string]int, len(entries))
	dependents := make(map[string][]string, len(entries))
	indexOf := make(map[string]int, len(entries))
	for i := range entries {
		indegree[entries[i].ID] = 0
		indexOf[entries[i].ID] = i
	}
	for i := range entries {
		id := entries[i].ID
		for _, dep := range deps[id] {
			indegree[id]++
			dependents[dep] = append(dependents[dep], id)
		}
	}
	ready := &readyHeap{}
	for i := range entries {
		if indegree[entries[i].ID] == 0 {
			heap.Push(ready, i)
		}
	}
	for ready.Len() > 0 {
		idx := heap.Pop(ready).(int)
		id := entries[idx].ID
		order = append(order, id)
		for _, next := range dependents[id] {
			indegree[next]--
			if indegree[next] == 0 {
				heap.Push(ready, indexOf[next])
			}
		}
	}
	if len(order) < len(entries) {
		for id, d := range indegree {
			if d > 0 {
				cyclic = append(cyclic, id)
			}
		}
		sort.Strings(cyclic)
		return nil, cyclic
	}
	return order, nil
}

// canonicalSourceKey 给出链接源端的规范化键：把同批次条目引用解析为其
// 将分配的确定性 RID，使条目引用与外部 RID 引用在别名情况下归为同组。
func canonicalSourceKey(ref Ref, entryByID map[string]*Entry) string {
	if ref.EntryID != "" {
		if dep, ok := entryByID[ref.EntryID]; ok {
			return "rid:" + assignedObjectRID(dep)
		}
		return "entry:" + ref.EntryID
	}
	return "rid:" + ref.ObjectRID
}

// markNonCandidates 基数预筛：按声明顺序扫描链接条目，对受基数约束的
// (源, 链接类型) 分组，只放行声明顺序最先出现者，其余标记为基数冲突。
// 该判定只依赖声明顺序，不依赖落地先后；开销为 O(条目数) 的哈希分组。
func markNonCandidates(entries []Entry, entryByID map[string]*Entry, s *Store) map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	nonCandidate := map[string]string{}
	firstOfGroup := map[string]string{} // (源,类型) -> 首个条目 ID
	for i := range entries {
		e := &entries[i]
		if e.Kind != EntryLink {
			continue
		}
		lt, ok := s.linkTypes[e.LinkTypeRID]
		if !ok || lt.MaxTargetsPerSource <= 0 {
			continue
		}
		key := canonicalSourceKey(e.Source, entryByID) + "|" + e.LinkTypeRID
		if first, dup := firstOfGroup[key]; dup {
			nonCandidate[e.ID] = fmt.Sprintf(
				"与声明顺序更早的条目 %s 在 (源, 链接类型 %s) 上违反基数约束，未放行", first, e.LinkTypeRID)
		} else {
			firstOfGroup[key] = e.ID
		}
	}
	return nonCandidate
}

// fail 记录一个失败判定。
func (ctx *importCtx) fail(id string, verdict Verdict, reason string) {
	ctx.results[id] = &EntryResult{EntryID: id, Verdict: verdict, Reason: reason}
}

// resolveRef 将引用解析为已落地对象的 RID。调用前需持存储锁。
func (imp *Importer) resolveRef(ctx *importCtx, ref Ref) (string, error) {
	s := imp.store
	if ref.EntryID != "" {
		depRes, ok := ctx.results[ref.EntryID]
		if !ok {
			return "", fmt.Errorf("引用了未知条目 %s", ref.EntryID)
		}
		if depRes.Verdict != VerdictSucceeded || depRes.ObjectRID == "" {
			return "", fmt.Errorf("引用的条目 %s 未成功落地", ref.EntryID)
		}
		return depRes.ObjectRID, nil
	}
	if ref.ObjectRID == "" {
		return "", fmt.Errorf("空引用")
	}
	if _, ok := s.objects[ref.ObjectRID]; !ok {
		return "", fmt.Errorf("引用的对象 %s 不存在", ref.ObjectRID)
	}
	return ref.ObjectRID, nil
}

// processOne 处理单个条目，判定优先级：
// 引用传播失败 > 批次内基数冲突 > 自身校验失败 > 落地时联合基数冲突。
// 调用前需持存储写锁。
func (imp *Importer) processOne(ctx *importCtx, id string) {
	e := ctx.entryByID[id]

	// 1. 引用传播：任一被引用的同批次条目已失败，则本条目不尝试落地，
	//    也不再计入它本可能引发的进一步校验。
	for _, depID := range entryDeps(e, ctx.entryByID) {
		depRes := ctx.results[depID]
		if depRes != nil && depRes.Verdict != VerdictSucceeded {
			ctx.fail(id, VerdictPropagatedFailed, fmt.Sprintf(
				"引用的同批次条目 %s 已失败（%s），本条目未尝试落地", depID, depRes.Verdict))
			return
		}
	}

	// 2. 批次内基数预筛：只放行声明顺序最先者。
	if reason, blocked := ctx.nonCandidate[id]; blocked {
		ctx.fail(id, VerdictCardinalityConflict, reason)
		return
	}

	// 3. 自身校验 + 落地。
	if e.Kind == EntryObject {
		imp.processObject(ctx, e)
	} else {
		imp.processLink(ctx, e)
	}
}

// processObject 校验并落地对象条目。调用前需持存储写锁。
func (imp *Importer) processObject(ctx *importCtx, e *Entry) {
	s := imp.store
	ot, ok := s.objectTypes[e.ObjectTypeRID]
	if !ok {
		ctx.fail(e.ID, VerdictValidationFailed, "未知对象类型 "+e.ObjectTypeRID)
		return
	}
	rid := assignedObjectRID(e)
	if _, exists := s.objects[rid]; exists {
		ctx.fail(e.ID, VerdictValidationFailed, "对象 RID 已存在: "+rid)
		return
	}
	// 解析并校验属性；Ref 值在此解析为已落地对象的 RID 后物化。
	materialized := make(map[string]any, len(e.Properties))
	for name, v := range e.Properties {
		spec, known := ot.spec(name)
		if !known {
			ctx.fail(e.ID, VerdictValidationFailed, "对象类型 "+ot.RID+" 未声明属性 "+name)
			return
		}
		switch spec.Type {
		case PropString:
			if _, ok := v.(string); !ok {
				ctx.fail(e.ID, VerdictValidationFailed, "属性 "+name+" 应为字符串")
				return
			}
			materialized[name] = v
		case PropInt:
			if _, ok := v.(int); !ok {
				ctx.fail(e.ID, VerdictValidationFailed, "属性 "+name+" 应为整数")
				return
			}
			materialized[name] = v
		case PropObjectRef:
			ref, ok := v.(Ref)
			if !ok {
				ctx.fail(e.ID, VerdictValidationFailed, "属性 "+name+" 应为对象引用")
				return
			}
			targetRID, err := imp.resolveRef(ctx, ref)
			if err != nil {
				ctx.fail(e.ID, VerdictValidationFailed, "属性 "+name+" 引用解析失败: "+err.Error())
				return
			}
			if spec.RefObjectType != "" {
				target := s.objects[targetRID]
				if target.TypeRID != spec.RefObjectType {
					ctx.fail(e.ID, VerdictValidationFailed, fmt.Sprintf(
						"属性 %s 引用对象类型应为 %s，实际为 %s", name, spec.RefObjectType, target.TypeRID))
					return
				}
			}
			materialized[name] = targetRID
		}
		if spec.Validate != nil {
			if err := spec.Validate(v); err != nil {
				ctx.fail(e.ID, VerdictValidationFailed, "属性 "+name+" 校验失败: "+err.Error())
				return
			}
		}
	}
	for _, spec := range ot.Properties {
		if spec.Required {
			if _, present := e.Properties[spec.Name]; !present {
				ctx.fail(e.ID, VerdictValidationFailed, "缺少必填属性 "+spec.Name)
				return
			}
		}
	}
	obj := &ObjectInstance{RID: rid, TypeRID: ot.RID, Properties: materialized}
	s.putObjectLocked(obj)
	ctx.results[e.ID] = &EntryResult{
		EntryID: e.ID, Verdict: VerdictSucceeded,
		Reason: "校验通过，对象已落地", ObjectRID: rid,
	}
	ctx.landed = append(ctx.landed, landedOp{entryID: e.ID, rid: rid, kind: EntryObject})
	ctx.pendingHooks = append(ctx.pendingHooks, rid)
}

// processLink 校验并落地链接条目，含与既有链接的联合基数校验（O(1) 索引访问）。
// 调用前需持存储写锁。
func (imp *Importer) processLink(ctx *importCtx, e *Entry) {
	s := imp.store
	lt, ok := s.linkTypes[e.LinkTypeRID]
	if !ok {
		ctx.fail(e.ID, VerdictValidationFailed, "未知链接类型 "+e.LinkTypeRID)
		return
	}
	srcRID, err := imp.resolveRef(ctx, e.Source)
	if err != nil {
		ctx.fail(e.ID, VerdictValidationFailed, "源端点解析失败: "+err.Error())
		return
	}
	tgtRID, err := imp.resolveRef(ctx, e.Target)
	if err != nil {
		ctx.fail(e.ID, VerdictValidationFailed, "目标端点解析失败: "+err.Error())
		return
	}
	if lt.SourceObjectType != "" && s.objects[srcRID].TypeRID != lt.SourceObjectType {
		ctx.fail(e.ID, VerdictValidationFailed, fmt.Sprintf(
			"源端点类型应为 %s，实际为 %s", lt.SourceObjectType, s.objects[srcRID].TypeRID))
		return
	}
	if lt.TargetObjectType != "" && s.objects[tgtRID].TypeRID != lt.TargetObjectType {
		ctx.fail(e.ID, VerdictValidationFailed, fmt.Sprintf(
			"目标端点类型应为 %s，实际为 %s", lt.TargetObjectType, s.objects[tgtRID].TypeRID))
		return
	}
	// 联合基数校验：与存储中已有链接（含本批次已落地者、批次外既有者、
	// 以及其它并发批次已落地者）联合判定。索引访问 O(1)，开销只随
	// 本批次涉及该源的条目数增长，不随该源已有链接总数增长。
	if lt.MaxTargetsPerSource > 0 {
		existing := s.linkCountLocked(srcRID, lt.RID)
		if existing+1 > lt.MaxTargetsPerSource {
			ctx.fail(e.ID, VerdictCardinalityConflict, fmt.Sprintf(
				"源实例 %s 上链接类型 %s 已有 %d 条，达到基数上限 %d",
				srcRID, lt.RID, existing, lt.MaxTargetsPerSource))
			return
		}
	}
	rid := "lnk-" + e.ID
	if _, exists := s.links[rid]; exists {
		ctx.fail(e.ID, VerdictValidationFailed, "链接 RID 已存在: "+rid)
		return
	}
	lnk := &LinkInstance{RID: rid, TypeRID: lt.RID, SourceRID: srcRID, TargetRID: tgtRID}
	s.putLinkLocked(lnk)
	ctx.results[e.ID] = &EntryResult{
		EntryID: e.ID, Verdict: VerdictSucceeded,
		Reason: "校验通过，链接已落地", LinkRID: rid,
	}
	ctx.landed = append(ctx.landed, landedOp{entryID: e.ID, rid: rid, kind: EntryLink})
	ctx.pendingHooks = append(ctx.pendingHooks, rid)
}

// rollbackIfNeeded 在 Atomic 模式下：若存在失败条目，则按落地逆序回滚全部
// 已落地条目。单条逆操作失败不中止流程，继续处理剩余条目并汇总。
// 调用前需持存储写锁。
func (imp *Importer) rollbackIfNeeded(ctx *importCtx) {
	anyFailure := false
	for _, res := range ctx.results {
		if res.Verdict != VerdictSucceeded {
			anyFailure = true
			break
		}
	}
	if !anyFailure {
		return
	}
	s := imp.store
	for i := len(ctx.landed) - 1; i >= 0; i-- {
		op := ctx.landed[i]
		var err error
		if op.kind == EntryLink {
			err = s.deleteLinkLocked(op.rid)
		} else {
			err = s.deleteObjectLocked(op.rid)
		}
		res := ctx.results[op.entryID]
		if err != nil {
			// 逆操作失败：实例仍存在于图中，保留 Succeeded 判定并汇总到报告。
			ctx.rollbackFailures = append(ctx.rollbackFailures, RollbackFailure{
				EntryID: op.entryID, RID: op.rid, Err: err.Error(),
			})
			continue
		}
		res.Verdict = VerdictRolledBack
		res.Reason = "曾成功落地，因请求内其它条目失败触发整体回滚而撤销"
	}
}
