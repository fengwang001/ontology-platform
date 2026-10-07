package compensate

import (
	"context"
	"sort"
	"sync"
	"time"
)

// branch 是一条分支的执行期可变状态。
type branch struct {
	name string
	spec *BranchSpec

	effects []*AppliedEffect // 已按声明顺序生效的子操作
	phase   branchPhase
	failAt  int // 自身失败的子操作下标；-1 表示无

	ready       chan struct{} // 所有依赖到达终态后关闭
	started     bool
	readyClosed bool
	recorded    bool

	// remainingDownstream：尚未完成补偿的、且有已生效副作用的直接下游分支数。
	// 判断该分支能否开始补偿只需读这一个整数，与动作内分支总数无关 -> O(1)。
	remainingDownstream int
	compStarted         bool
	compDone            chan struct{} // nil 表示该分支无副作用、不参与补偿
	compFailedOps       []int
	compensatedOps      []int
}

// runtime 是一次 Execute 的全部可变状态；执行/补偿/外部直接请求都作用于它。
type runtime struct {
	action *Action
	graph  *Graph
	logger Logger

	mu       sync.Mutex
	branches map[string]*branch
	order    []string

	recordsMu sync.Mutex
	records   []ErrorRecord

	applyDone      chan struct{}
	applyClosed    bool
	pendingRecords []ErrorRecord // 锁内收集、applyDone 关闭后统一追加的记录
	anyFailed      bool

	compOnce     sync.Once
	compFinished chan struct{}
	compMu       sync.Mutex
	cond         *sync.Cond
	queue        []string
	inflight     int
	blocked      map[string]bool
}

func emptyReport(actionID string) *Report {
	return &Report{ActionID: actionID, Applied: map[string][]int{}, Compensated: map[string][]int{}}
}

// Execute 并发生效所有分支；任一分支最终失败时向下游传播并自动补偿全部已生效副作用。
func (a *Action) Execute(ctx context.Context) *Report {
	rt := a.newRuntime()
	rt.runApply(ctx)
	// 仅当存在最终失败的分支（自身失败或被动失败）时才补偿；全成功动作保留已生效副作用。
	rt.mu.Lock()
	failed := rt.anyFailed
	rt.mu.Unlock()
	if failed {
		// 失败：等待补偿调度完全结束。锁由 pump 收口时统一释放，此处不得提前释放。
		rt.startCompensation()
	} else {
		// 全成功：无补偿阶段，在提交点释放严格 2PL 锁。
		rt.graph.ReleaseAll(a.spec.ID)
	}
	return rt.report()
}

// Compensate 幂等地补偿整个动作；Execute 失败时已自动补偿过，重复调用安全。
func (a *Action) Compensate(ctx context.Context) *Report {
	if a.rt == nil {
		return emptyReport(a.spec.ID)
	}
	a.rt.startCompensation()
	return a.rt.report()
}

// RequestDirectCompensation 处理外部对单条分支的直接补偿请求。
// 允许当且仅当该分支全部有副作用的下游依赖方都已完成补偿（remainingDownstream==0）；
// 否则以 KindCompensationDenied 拒绝，拒绝不改变任何已生效状态。
func (a *Action) RequestDirectCompensation(branchName string) error {
	if a.rt == nil {
		return ErrorRecord{Kind: KindCompensationDenied, ActionID: a.spec.ID, Branch: branchName,
			OpIndex: -1, Message: "action has not executed; nothing to compensate"}
	}
	rt := a.rt
	<-rt.applyDone
	// 外部直接请求可能早于自动补偿调度器（例如只跑到生效阶段）。
	// 先幂等确保调度器存在，但不等待全量完成，否则该分支尚不能补偿时会自死锁。
	rt.ensurePump()
	rt.mu.Lock()
	b, ok := rt.branches[branchName]
	if !ok {
		rt.mu.Unlock()
		return ErrorRecord{Kind: KindCompensationDenied, ActionID: a.spec.ID, Branch: branchName,
			OpIndex: -1, Message: "unknown branch"}
	}
	if len(b.effects) == 0 {
		rt.mu.Unlock()
		return nil
	}
	if !b.compStarted {
		if b.remainingDownstream > 0 {
			rt.mu.Unlock()
			rec := ErrorRecord{Kind: KindCompensationDenied, ActionID: a.spec.ID, Branch: branchName,
				OpIndex: -1, Message: "direct compensation denied: downstream dependent branches not yet compensated"}
			rt.appendLog(AttemptEvent{Phase: "direct_compensate_request", Branch: branchName, OpIndex: -1,
				Input:  "compensate branch before its downstream branches",
				Result: "denied; no applied state changed", Reason: "remainingDownstream>0", Allowed: false})
			rt.addRecord(rec)
			return rec
		}
		done := b.compDone
		rt.mu.Unlock()
		rt.enqueue([]string{branchName})
		<-done
		return rt.directBranchError(branchName)
	}
	done := b.compDone
	rt.mu.Unlock()
	<-done
	rt.mu.Lock()
	stillOrdered := b.remainingDownstream > 0
	rt.mu.Unlock()
	if stillOrdered {
		rec := ErrorRecord{Kind: KindCompensationDenied, ActionID: a.spec.ID, Branch: branchName,
			OpIndex: -1, Message: "direct compensation denied: downstream dependent branches not fully compensated"}
		rt.appendLog(AttemptEvent{Phase: "direct_compensate_request", Branch: branchName, OpIndex: -1,
			Input:  "compensate branch whose downstream failed to compensate",
			Result: "denied; no applied state changed", Reason: "remainingDownstream>0", Allowed: false})
		rt.addRecord(rec)
		return rec
	}
	err := rt.directBranchError(branchName)
	if err == nil {
		return nil
	}
	// 分支已结束但存在失败逆操作：对直接请求统一报 KindCompensationDenied，
	// 底层 INVERSE_FAILED 记录仍完整保留在 Report 中，不被遮蔽。
	rec := ErrorRecord{Kind: KindCompensationDenied, ActionID: a.spec.ID, Branch: branchName,
		OpIndex: -1, Message: "direct compensation cannot complete: " + err.Error()}
	rt.addRecord(rec)
	return rec
}

func (rt *runtime) directBranchError(branchName string) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	for _, idx := range rt.branches[branchName].compFailedOps {
		if rec := rt.findRecordLocked(KindInverseFailed, branchName, idx); rec != nil {
			return *rec
		}
	}
	return nil
}

func (rt *runtime) findRecordLocked(kind ErrKind, branchName string, opIndex int) *ErrorRecord {
	rt.recordsMu.Lock()
	defer rt.recordsMu.Unlock()
	for i := range rt.records {
		r := &rt.records[i]
		if r.Kind == kind && r.Branch == branchName && r.OpIndex == opIndex {
			return r
		}
	}
	return nil
}

// ---------------- 生效阶段 ----------------

func (rt *runtime) runApply(ctx context.Context) {
	// 动作级严格 2PL：在任何子操作生效前，一次性获取本动作声明的全部写入槽位。
	// 按槽位全局排序加锁以避免跨动作加锁顺序环；同一动作对槽位去重。
	// 锁持有到全部补偿结束。这样在重叠槽位上两个动作不会交错提交/恢复，
	// 彼此捕获的确定性逆操作始终有效，保证跨动作可串行化。
	slots := rt.action.allWriteSlots()
	for _, key := range slots {
		rt.graph.Acquire(rt.action.spec.ID, key.objectID, key.property)
	}
	for _, name := range rt.order {
		go rt.runBranch(ctx, rt.branches[name])
	}
	<-rt.applyDone
	// 生效全部终态后，根据最终 phase 幂等收集所有被动失败分支的独立记录。
	// 此处所有 goroutine 已停止修改 phase（applyDone 之后有 happens-before），
	// 不依赖具体调度时机，保证与朴素串行模型的传递失败计数一致。
	rt.mu.Lock()
	for _, name := range rt.order {
		b := rt.branches[name]
		if b.phase == bpFailedUpstream && !b.recorded {
			b.recorded = true
			rt.pendingRecords = append(rt.pendingRecords, ErrorRecord{
				Kind: KindUpstreamFailed, ActionID: rt.action.spec.ID, Branch: name, OpIndex: -1,
				Message: "branch never started: upstream branch terminally failed"})
		}
	}
	rt.mu.Unlock()
	if len(rt.pendingRecords) > 0 {
		rt.recordsMu.Lock()
		rt.records = append(rt.records, rt.pendingRecords...)
		rt.recordsMu.Unlock()
		rt.pendingRecords = nil
	}
}

func (rt *runtime) runBranch(ctx context.Context, b *branch) {
	// 等待全部声明依赖都到达终态。任一依赖最终失败 => 本分支被动失败，
	// 尚未开始的子操作永不开始。先等齐再判定，避免在多依赖交错下漏判传递失败。
	failedDep := ""
	for _, dep := range b.spec.Deps {
		select {
		case <-ctx.Done():
			return
		case <-rt.branches[dep].ready:
		}
		rt.mu.Lock()
		depPhase := rt.branches[dep].phase
		rt.mu.Unlock()
		if depPhase != bpApplied && failedDep == "" {
			failedDep = dep
		}
	}
	if failedDep != "" {
		rt.markUpstreamFailed(b, failedDep)
		return
	}

	rt.mu.Lock()
	b.started = true
	b.phase = bpRunning
	rt.mu.Unlock()

	for i := range b.spec.Ops {
		op := b.spec.Ops[i]
		rt.appendLog(AttemptEvent{Phase: "apply", Branch: b.name, OpIndex: i, OpID: op.ID,
			Input: op.label(), Reason: "dependencies applied; ops run in declared order", Allowed: true})
		if op.FailApply {
			rt.appendLog(AttemptEvent{Phase: "apply", Branch: b.name, OpIndex: i, OpID: op.ID,
				Input: op.label(), Result: "sub-operation failed",
				Reason: "injected apply failure", Allowed: true})
			rt.markSelfFailed(b, i, op)
			return
		}
		rt.graph.Acquire(rt.action.spec.ID, op.ObjectID, op.Property)
		undo := rt.graph.CommitSet(rt.action.spec.ID, op.ObjectID, op.Property, op.Value)
		rt.mu.Lock()
		b.effects = append(b.effects, &AppliedEffect{Branch: b.name, Index: i, Op: op, Undo: undo})
		rt.mu.Unlock()
		rt.appendLog(AttemptEvent{Phase: "apply", Branch: b.name, OpIndex: i, OpID: op.ID,
			Input: op.label(), Result: "applied; deterministic inverse captured",
			Reason: "commit under action 2PL lock", Allowed: true})
	}

	rt.mu.Lock()
	b.phase = bpApplied
	rt.finishBranchLocked(b)
	rt.mu.Unlock()
}

func (rt *runtime) markSelfFailed(b *branch, idx int, op WriteOp) {
	rt.mu.Lock()
	b.phase = bpFailedSelf
	b.failAt = idx
	rt.finishBranchLocked(b)
	rt.mu.Unlock()
	rt.addRecord(ErrorRecord{Kind: KindSubOperationFailed, ActionID: rt.action.spec.ID,
		Branch: b.name, OpIndex: idx, OpID: op.ID, Message: "sub-operation failed during apply"})
}

func (rt *runtime) markUpstreamFailed(b *branch, dep string) {
	rt.mu.Lock()
	b.phase = bpFailedUpstream
	b.failAt = -1
	rt.finishBranchLocked(b)
	// 失败沿依赖边向下游传递闭包：所有把 b（传递）作为上游、尚未到达终态的分支，
	// 一律立即标记为被动失败并关闭其 ready，其尚未开始的子操作永不开始。
	rt.propagateFailureLocked(b.name)
	rt.mu.Unlock()
	rt.appendLog(AttemptEvent{Phase: "apply", Branch: b.name, OpIndex: -1,
		Result: "passively failed; no sub-operation started",
		Reason: "upstream " + dep + " failed", Allowed: false})
}

// propagateFailureLocked 从 failedName 的直接下游出发做 BFS，把未终态分支标记为被动失败。
// 调用方持有 rt.mu。错误记录在释放锁后统一补登（避免锁内追加切片），由调用批处理；
// 这里只负责状态与 ready 关闭，记录由 collectUpstreamRecords 生成。
func (rt *runtime) propagateFailureLocked(failedName string) {
	queue := append([]string{}, rt.action.decl.dependents[failedName]...)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		cb := rt.branches[cur]
		if cb == nil || terminalPhases[cb.phase] {
			continue
		}
		cb.phase = bpFailedUpstream
		cb.failAt = -1
		if !cb.readyClosed {
			cb.readyClosed = true
			close(cb.ready)
		}
		rt.finishBranchLocked(cb)
		queue = append(queue, rt.action.decl.dependents[cur]...)
	}
}

var terminalPhases = map[branchPhase]bool{
	bpApplied: true, bpFailedSelf: true, bpFailedUpstream: true,
}

// branchTerminal 在每条分支进入终态时调用；全部终态后关闭 applyDone。
// finishBranchLocked 在同一临界区内完成“置终态 + 统计 + 关闭 applyDone”，
// 消除“终态已写但尚未计入”的窗口。调用方持有 rt.mu。
func (rt *runtime) finishBranchLocked(b *branch) {
	if b.phase == bpFailedSelf || b.phase == bpFailedUpstream {
		rt.anyFailed = true
	}
	done := 0
	for _, x := range rt.branches {
		if terminalPhases[x.phase] {
			done++
		}
	}
	if done == len(rt.branches) && !rt.applyClosed {
		rt.applyClosed = true
		rt.collectUpstreamRecordsLocked()
		close(rt.applyDone)
	}
	if !b.readyClosed {
		b.readyClosed = true
		close(b.ready)
	}
}

// collectUpstreamRecordsLocked 在生效阶段收口时，为每个被动失败分支补登一条独立的
// UPSTREAM_FAILED 记录（互不遮蔽）。由 finishBranchLocked 在关闭 applyDone 时调用。
func (rt *runtime) collectUpstreamRecordsLocked() {
	for _, name := range rt.order {
		b := rt.branches[name]
		if b.phase == bpFailedUpstream && !b.recorded {
			b.recorded = true
			rt.pendingRecords = append(rt.pendingRecords, ErrorRecord{
				Kind: KindUpstreamFailed, ActionID: rt.action.spec.ID, Branch: name, OpIndex: -1,
				Message: "branch never started: upstream branch terminally failed"})
		}
	}
}

// ---------------- 补偿阶段 ----------------

func (rt *runtime) startCompensation() {
	rt.ensurePump()
	<-rt.compFinished
}

// ensurePump 幂等地完成补偿初始化并启动调度器 goroutine（不等待其结束）。
func (rt *runtime) ensurePump() {
	rt.compOnce.Do(func() {
		<-rt.applyDone
		rt.initCompState()
		go rt.pump()
	})
}

// initCompState 仅做 O(分支数) 的一次性初始化：
// 每条分支的 remainingDownstream = 有已生效副作用的直接下游数。
// 之后判定“某分支能否开始补偿”只需读它自己的 remainingDownstream，单次判定 O(1)，
// 不随动作声明的分支总数线性增长。
func (rt *runtime) initCompState() {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.blocked = map[string]bool{}
	for name, b := range rt.branches {
		n := 0
		for _, down := range rt.action.decl.dependents[name] {
			if len(rt.branches[down].effects) > 0 {
				n++
			}
		}
		b.remainingDownstream = n
		if len(b.effects) > 0 {
			b.compDone = make(chan struct{})
		}
	}
	initial := make([]string, 0)
	for _, name := range rt.order {
		b := rt.branches[name]
		if len(b.effects) > 0 && b.remainingDownstream == 0 {
			initial = append(initial, name)
		}
	}
	sort.Strings(initial)
	rt.queue = initial
}

// enqueue 供外部直接补偿请求加入调度；最终可执行状态在锁内复核。
func (rt *runtime) enqueue(names []string) {
	rt.compMu.Lock()
	defer rt.compMu.Unlock()
	rt.mu.Lock()
	added := false
	for _, n := range names {
		b := rt.branches[n]
		if b == nil || b.compStarted || b.compDone == nil {
			continue
		}
		if len(b.effects) > 0 && b.remainingDownstream == 0 {
			rt.queue = append(rt.queue, n)
			added = true
		}
	}
	rt.mu.Unlock()
	if added {
		rt.cond.Broadcast()
	}
}

// pump 是补偿调度器。当前满足条件的多条无依赖分支可同时在飞（明确允许并发补偿）：
// 其写入集在声明期已保证互不相交，逆序恢复操作彼此可交换，
// 因此无论各分支补偿相对快慢，最终对象图都等价于某个合法拓扑顺序的串行补偿。
func (rt *runtime) pump() {
	var wg sync.WaitGroup
	rt.compMu.Lock()
	for {
		for len(rt.queue) == 0 && rt.inflight > 0 {
			rt.cond.Wait()
		}
		if len(rt.queue) == 0 && rt.inflight == 0 {
			rt.compMu.Unlock()
			// 必须等待所有补偿 goroutine（含其 Restore）真正返回后再放锁：
			// inflight 归零与 goroutine 退出之间存在窗口，提前放锁会让其它动作
			// 在本动作最后一个逆操作尚未完成时介入，破坏跨动作串行化。
			wg.Wait()
			rt.graph.ReleaseAll(rt.action.spec.ID)
			close(rt.compFinished)
			return
		}
		name := rt.queue[0]
		rt.queue = rt.queue[1:]
		rt.mu.Lock()
		b := rt.branches[name]
		launch := b.compDone != nil && !b.compStarted
		if launch {
			b.compStarted = true
			rt.inflight++
		}
		rt.mu.Unlock()
		if !launch {
			continue
		}
		wg.Add(1)
		go func(b *branch) {
			defer wg.Done()
			rt.compensateBranch(b)
		}(b)
	}
}

// compensateBranch 对一条分支执行已生效子操作的严格逆序补偿。
// 逆操作失败独立记录到具体“分支+子操作”，不中止本分支其余逆操作、不影响其它分支，
// 故多分支同时补偿失败时信息互不遮蔽；失败分支会永久阻塞其上游祖先（逆序约束）。
func (rt *runtime) compensateBranch(b *branch) {
	rt.mu.Lock()
	b.phase = bpCompensating
	rt.mu.Unlock()

	failedAny := false
	compensated := []int{}
	for i := len(b.effects) - 1; i >= 0; i-- {
		eff := b.effects[i]
		op := eff.Op
		rt.appendLog(AttemptEvent{Phase: "compensate", Branch: b.name, OpIndex: i, OpID: op.ID,
			Input:  "inverse of " + op.label(),
			Reason: "all downstream branches compensated; reverse order within branch", Allowed: true})
		if op.FailCompensate {
			failedAny = true
			rt.appendLog(AttemptEvent{Phase: "compensate", Branch: b.name, OpIndex: i, OpID: op.ID,
				Input: "inverse of " + op.label(), Result: "inverse operation failed",
				Reason: "injected compensate failure; other ops/branches unaffected", Allowed: true})
			rt.mu.Lock()
			b.compFailedOps = append(b.compFailedOps, i)
			rt.mu.Unlock()
			rt.addRecord(ErrorRecord{Kind: KindInverseFailed, ActionID: rt.action.spec.ID,
				Branch: b.name, OpIndex: i, OpID: op.ID,
				Message: "inverse operation failed; effect at this slot remains"})
			continue
		}
		rt.graph.Restore(rt.action.spec.ID, eff.Undo)
		compensated = append(compensated, i)
		rt.appendLog(AttemptEvent{Phase: "compensate", Branch: b.name, OpIndex: i, OpID: op.ID,
			Input: "inverse of " + op.label(), Result: "restored previous value",
			Reason: "deterministic inverse restore", Allowed: true})
	}

	rt.mu.Lock()
	if failedAny {
		b.phase = bpCompensateFailed
	} else {
		b.phase = bpCompensated
	}
	b.compensatedOps = compensated
	rt.mu.Unlock()

	// 该分支完成补偿：递减其直接上游的 remainingDownstream（O(1)/每条边），
	// 任一上游因此归零时即满足开始补偿条件。若本分支逆操作失败，
	// 则不递减（其上游永不满足逆序条件，被记录为 Blocked）。
	var newlyReady []string
	rt.mu.Lock()
	if !failedAny {
		for _, up := range b.spec.Deps {
			ub := rt.branches[up]
			ub.remainingDownstream--
			if ub.remainingDownstream == 0 && len(ub.effects) > 0 && !ub.compStarted {
				newlyReady = append(newlyReady, up)
			}
		}
	}
	rt.mu.Unlock()

	rt.compMu.Lock()
	if !failedAny && len(newlyReady) > 0 {
		sort.Strings(newlyReady)
		rt.queue = append(rt.queue, newlyReady...)
	}
	if failedAny {
		for up := range ancestors(b.name, rt.action.decl.byName) {
			rt.blocked[up] = true
		}
	}
	rt.inflight--
	rt.finishCheckLocked()
	rt.cond.Broadcast()
	rt.compMu.Unlock()
	// compDone 必须在所有调度簿记（递减计数、入队上游、阻塞标记）完成后再关闭，
	// 保证任何等到 compDone 的观察者看到的都是最终一致状态。
	close(b.compDone)
}

// finishCheckLocked 在队列空且无在飞补偿时，把被永久阻塞的上游标记完成结算，
// 避免调度器等待永不会就绪的分支。调用方持有 compMu。
func (rt *runtime) finishCheckLocked() {
	if len(rt.queue) != 0 || rt.inflight != 0 {
		return
	}
	rt.mu.Lock()
	for name, b := range rt.branches {
		if b.compDone == nil {
			continue
		}
		if !b.compStarted {
			b.compStarted = true
			rt.blocked[name] = true
			close(b.compDone)
		}
	}
	rt.mu.Unlock()
}

// ---------------- runtime 构造与汇总 ----------------

func (a *Action) newRuntime() *runtime {
	rt := &runtime{
		action:       a,
		graph:        a.engine.graph,
		logger:       a.engine.logger,
		branches:     map[string]*branch{},
		order:        a.decl.order,
		applyDone:    make(chan struct{}),
		compFinished: make(chan struct{}),
		blocked:      map[string]bool{},
	}
	rt.cond = sync.NewCond(&rt.compMu)
	for i := range a.spec.Branches {
		s := &a.spec.Branches[i]
		rt.branches[s.Name] = &branch{
			name:   s.Name,
			spec:   s,
			phase:  bpPending,
			failAt: -1,
			ready:  make(chan struct{}),
		}
	}
	a.rt = rt
	return rt
}

func (rt *runtime) addRecord(r ErrorRecord) {
	rt.recordsMu.Lock()
	rt.records = append(rt.records, r)
	rt.recordsMu.Unlock()
}

func (rt *runtime) appendLog(ev AttemptEvent) {
	if rt.logger == nil {
		return
	}
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	ev.ActionID = rt.action.spec.ID
	rt.logger.LogAttempt(ev)
}

// Report 汇总一次动作的执行/补偿结果。
type Report struct {
	ActionID    string
	Records     []ErrorRecord
	Applied     map[string][]int // 分支 -> 已生效子操作下标（升序）
	Compensated map[string][]int // 分支 -> 已成功补偿子操作下标（升序）
	Blocked     []string         // 因下游补偿失败而永远无法开始补偿的上游分支
}

func (rt *runtime) report() *Report {
	rep := &Report{
		ActionID:    rt.action.spec.ID,
		Applied:     map[string][]int{},
		Compensated: map[string][]int{},
	}
	rt.recordsMu.Lock()
	rep.Records = append(rep.Records, rt.records...)
	rt.recordsMu.Unlock()

	rt.mu.Lock()
	for _, name := range rt.order {
		b := rt.branches[name]
		if len(b.effects) > 0 {
			idx := make([]int, 0, len(b.effects))
			for _, eff := range b.effects {
				idx = append(idx, eff.Index)
			}
			rep.Applied[name] = idx
		}
		if len(b.compensatedOps) > 0 {
			done := append([]int{}, b.compensatedOps...)
			sort.Ints(done)
			rep.Compensated[name] = done
		}
	}
	for name := range rt.blocked {
		rep.Blocked = append(rep.Blocked, name)
	}
	rt.mu.Unlock()
	sort.Strings(rep.Blocked)

	// 固定优先级：依赖环拒绝 > 被动失败 > 自身失败 > 直接补偿拒绝 > 逆操作失败。
	sort.SliceStable(rep.Records, func(i, j int) bool {
		if rep.Records[i].Kind != rep.Records[j].Kind {
			return kindPriority(rep.Records[i].Kind) < kindPriority(rep.Records[j].Kind)
		}
		if rep.Records[i].Branch != rep.Records[j].Branch {
			return rep.Records[i].Branch < rep.Records[j].Branch
		}
		return rep.Records[i].OpIndex < rep.Records[j].OpIndex
	})
	return rep
}

// Primary 返回固定优先级下最高的一条错误记录；无记录返回 nil。
func (r *Report) Primary() *ErrorRecord {
	if r == nil || len(r.Records) == 0 {
		return nil
	}
	return &r.Records[0]
}

// RecordsByKind 按错误类别提取记录。
func (r *Report) RecordsByKind(kind ErrKind) []ErrorRecord {
	var out []ErrorRecord
	for _, rec := range r.Records {
		if rec.Kind == kind {
			out = append(out, rec)
		}
	}
	return out
}
