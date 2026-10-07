package compensation

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// 关于"无依赖关系的分支是否允许并发补偿"的明确结论：允许。
//
// 理由：
// 1. 语义上它们之间没有补偿先后约束，需求对其相对次序不做限制；
// 2. 对象图访问受 LockManager 的键级锁约束：触碰不相交键集的分支
//    物理并行；触碰同一键的子操作使用 Add 这类交换律原语，任一
//    交织的净效应相同；
// 3. 因此并发补偿的终态必然等价于"存在至少一个合法拓扑顺序逐一
//    补偿"的终态，与各分支补偿的相对快慢无关；
// 4. 每个分支内部仍严格按已生效子操作逆序逐个执行逆操作。

// CanCompensate 判断分支现在是否满足开始补偿的条件。
//
// 该判断只读取该分支自身的 remainingDownstream 计数与状态，
// 时间复杂度 O(1)，不随动作的分支总数增长，也不遍历任何分支集合。
// 计数在其直接下游分支完成补偿时以原子减一维护（反向拓扑边的
// 入度），计数归零即代表逆向 DAG 中该分支已无未处理前驱。
func (a *Action) CanCompensate(branch string) bool {
	st, ok := a.branches[branch]
	if !ok {
		return false
	}
	st.compMu.Lock()
	done := st.compensated || st.compRunning
	st.compMu.Unlock()
	if done || len(st.applied) == 0 {
		return false
	}
	return atomic.LoadInt32(a.remainingDownstream[branch]) == 0
}

// compensateAll 在正向阶段出现失败后自动补偿全部已生效副作用。
// 无依赖关系的就绪分支并发补偿；分支内逆序；失败各自独立记录。
func (a *Action) compensateAll(rep *Report) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, bname := range a.order {
		if len(a.branches[bname].applied) == 0 {
			// 没有任何已生效子操作：直接视为补偿完成，并放行其上游。
			a.markCompensated(bname)
			continue
		}
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			errs := a.compensateBranchWhenReady(name, nil)
			if len(errs) > 0 {
				mu.Lock()
				rep.Errors = append(rep.Errors, errs...)
				mu.Unlock()
			}
		}(bname)
	}
	wg.Wait()
}

// DirectCompensate 处理外部对单条分支的直接补偿请求（区别于失败
// 传播触发的自动补偿）。
//
// 若该分支仍有任一下游分支未完成补偿，请求被拒绝：返回
// KindCompensationOrderViolation 错误，且该分支及其依赖方的任何
// 已生效状态都不改变（不执行任何逆操作，门禁计数不变）。
//
// 该方法会先像 Execute 一样获取动作的键级锁（与其他动作串行化），
// 补偿结束后立即释放。正向阶段已经失败并自动补偿过的动作再次
// 直接请求补偿时，返回空报告（幂等，无状态变化）。
func (a *Action) DirectCompensate(branch string) *Report {
	st, ok := a.branches[branch]
	if !ok {
		return &Report{Errors: []*BranchError{{
			Kind:       KindCompensationOrderViolation,
			BranchName: branch,
			StepIndex:  -1,
			Detail:     "unknown-branch",
		}}}
	}

	blockers := a.downstreamBlocking(branch)
	if len(blockers) > 0 {
		err := &BranchError{
			Kind:       KindCompensationOrderViolation,
			BranchName: branch,
			StepIndex:  -1,
			Cause:      &orderViolationError{branch: branch, blockers: blockers},
		}
		a.logAttempt(branch, -1, "", "direct-compensate", false, "rejected",
			"blocked-by="+fmt.Sprint(blockers))
		return &Report{Errors: []*BranchError{err}}
	}

	st.compMu.Lock()
	if st.compensated || len(st.applied) == 0 {
		st.compMu.Unlock()
		return nil
	}
	if st.compRunning {
		st.compMu.Unlock()
		return nil
	}
	st.compRunning = true
	st.compMu.Unlock()

	token := a.locks.LockAll(a.id, a.allKeys())
	errs := a.runUndo(branch, nil)
	a.finishCompensated(branch)
	a.locks.UnlockAll(token)

	if len(errs) == 0 {
		return nil
	}
	return &Report{Errors: errs}
}

// downstreamBlocking 返回尚未完成补偿的直接下游分支名。
// 为生成拒绝原因而做 O(下游数) 枚举；门禁"判断本身"
// （CanCompensate / 计数归零等待）不做该枚举，保持 O(1)。
func (a *Action) downstreamBlocking(branch string) []string {
	var blockers []string
	for _, d := range a.downstream[branch] {
		st := a.branches[d]
		st.compMu.Lock()
		ok := st.compensated
		st.compMu.Unlock()
		if !ok {
			blockers = append(blockers, d)
		}
	}
	return blockers
}

// compensateBranchWhenReady 阻塞直到门禁放行，然后独占执行该分支
// 的逆序补偿。多个调用方竞争时只有一个真正执行，其余无副作用返回。
func (a *Action) compensateBranchWhenReady(branch string, triggerCause error) []*BranchError {
	st := a.branches[branch]

	st.compMu.Lock()
	if st.compensated {
		st.compMu.Unlock()
		return nil
	}
	if st.compRunning {
		st.compMu.Unlock()
		// 与正在执行的补偿并发：等待其结束广播，结果已登记在
		// st.undoErrors，调用方共享同一份失败记录，不重复执行逆操作。
		<-a.compDoneCh[branch]
		return append([]*BranchError(nil), st.undoErrors...)
	}
	st.compRunning = true
	st.compMu.Unlock()

	a.waitGateZero(branch)
	errs := a.runUndo(branch, triggerCause)
	a.finishCompensated(branch)
	return errs
}

// waitGateZero O(1) 地等待"剩余未补偿下游数"归零：
// 每次只原子读取该分支自己的计数器，不归零则阻塞在它自己的
// 广播 channel 上。整个过程不遍历分支集合。
func (a *Action) waitGateZero(branch string) {
	for {
		// 计数与广播 channel 必须在同一把锁下一起快照，
		// 否则可能读到"旧计数 + 新 channel"而永久漏唤醒。
		a.gateMu.Lock()
		remaining := atomic.LoadInt32(a.remainingDownstream[branch])
		ch := a.gateChanged[branch]
		a.gateMu.Unlock()
		if remaining == 0 {
			return
		}
		<-ch
	}
}

// runUndo 按已生效子操作的逆序逐个执行逆操作。
// 即使某个逆操作失败也继续执行剩余逆操作（best-effort），
// 每个失败分别记录，保证多分支/多子操作的失败互不遮蔽。
func (a *Action) runUndo(branch string, triggerCause error) []*BranchError {
	st := a.branches[branch]
	var errs []*BranchError
	for i := len(st.applied) - 1; i >= 0; i-- {
		step := st.applied[i]
		if step.inv == nil {
			a.logAttempt(branch, step.index, step.name, "no-inverse", true, "ok",
				"downstream-remaining=0; inverse=nil skipped")
			continue
		}
		input := describeKeys(step.inv.Keys())
		err := step.inv.Undo(a.graph)
		if err != nil {
			be := &BranchError{
				Kind:       KindUndoFailure,
				BranchName: branch,
				StepIndex:  step.index,
				Cause:      err,
			}
			errs = append(errs, be)
			st.undoErrors = append(st.undoErrors, be)
			a.logAttempt(branch, step.index, step.inv.Name(), input, true,
				"undo-error", "downstream-remaining=0; "+err.Error())
			continue
		}
		a.logAttempt(branch, step.index, step.inv.Name(), input, true, "ok",
			"downstream-remaining=0")
	}
	return errs
}

// markCompensated 用于没有任何已生效副作用的分支：直接放行其上游。
func (a *Action) markCompensated(branch string) {
	st := a.branches[branch]
	st.compMu.Lock()
	if st.compensated {
		st.compMu.Unlock()
		return
	}
	st.compensated = true
	st.compMu.Unlock()
	a.decrementUpstreamGates(branch)
	close(a.compDoneCh[branch])
}

// finishCompensated 标记分支补偿完成，并把它从所有直接依赖分支的
// 门禁计数中减一；计数归零则广播唤醒等待者。
func (a *Action) finishCompensated(branch string) {
	st := a.branches[branch]
	st.compMu.Lock()
	st.compensated = true
	st.compRunning = false
	st.compMu.Unlock()
	a.decrementUpstreamGates(branch)
	close(a.compDoneCh[branch])
}

// decrementUpstreamGates 只触达本分支显式依赖的上游（分支自身的
// 依赖声明长度），不遍历全部分支。
func (a *Action) decrementUpstreamGates(branch string) {
	for _, up := range a.branches[branch].spec.DependsOn {
		if atomic.AddInt32(a.remainingDownstream[up], -1) == 0 {
			a.gateMu.Lock()
			ch := a.gateChanged[up]
			a.gateChanged[up] = make(chan struct{})
			a.gateMu.Unlock()
			close(ch)
		}
	}
}

func (a *Action) logAttempt(branch string, step int, invName, input string,
	allowed bool, result, reason string) {
	if a.logger == nil {
		return
	}
	a.logger.LogAttempt(LogEntry{
		ActionName:  a.name,
		BranchName:  branch,
		StepIndex:   step,
		InverseName: invName,
		Input:       input,
		Allowed:     allowed,
		Result:      result,
		Reason:      reason,
	})
}

func describeKeys(keys []string) string {
	return fmt.Sprintf("keys=%v", keys)
}
