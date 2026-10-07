package chain

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Invariant 是共享对象必须保持的全局不变量。
// 在每次链条提交后基于最新持久化状态重新评估；不通过则提交被拒绝。
type Invariant func(state map[string]map[string]any) (pass bool, violated []string)

// maxReplays 限制单条链条因并发冲突而重放重算的次数。
const maxReplays = 64

// Engine 是并发执行引擎：多条独立调用链条可并发计算，
// 但提交点全局串行化，提交结果等价于按同一全序逐条串行执行。
type Engine struct {
	reg       *Registry
	store     *ObjectStore
	invariant Invariant

	commitMu sync.Mutex // 提交全序锁
	version  int        // 已成功提交的链条数（持久化状态版本号）
	order    []string   // 已提交链条 ID 的全序
}

// NewEngine 创建并发引擎。invariant 可为 nil（不检查）。
func NewEngine(reg *Registry, store *ObjectStore, inv Invariant) *Engine {
	return &Engine{reg: reg, store: store, invariant: inv}
}

// ChainResult 是一条链条执行的对外结果。
type ChainResult struct {
	Record      *ChainRecord
	CommitOrder int // 该链条在提交全序中的位置（未提交为 -1）
	Replays     int // 因并发冲突重放重算的次数
}

// Run 执行一条顶层调用链条。
//
// 可串行化方案（乐观提交 + 提交点串行化）：
//  1. 链条在当前持久化快照上纯函数地计算写入计划，计算过程不持锁、可并发；
//  2. 提交时进入全局临界区：若期间版本号已推进，则放弃本次计算结果，
//     基于最新快照整条重放重算（前置条件、自我触发检测全部重新来一遍），
//     因而重放链条观察到的正是“前序链条已全部提交”的串行状态；
//  3. 在同一临界区内评估全局不变量并提交、推进版本号。
//
// 最终每个共享对象上的写入等价于按提交全序逐条串行执行。
func (e *Engine) Run(ctx context.Context, chainID, entry string, input Params) (*ChainResult, error) {
	if err := e.reg.ValidateChain(entry); err != nil {
		return &ChainResult{
			Record: &ChainRecord{
				Entry: entry, Input: input, Status: StatusDeclarationError,
				Committed: false, CommitIndex: -1,
				StartedAt: time.Now(), FinishedAt: time.Now(),
			},
			CommitOrder: -1,
		}, err
	}

	started := time.Now()
	for replay := 0; ; replay++ {
		if replay > maxReplays {
			return nil, fmt.Errorf("chain %q exceeded max replays (%d)", chainID, maxReplays)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		snapshot, baseVersion := e.readState()
		x := &executor{reg: e.reg}
		top := x.run(entry, input, NewView(snapshot))

		e.commitMu.Lock()
		if e.version != baseVersion {
			// 已有其它链条提交：放弃本次全部计算结果，基于最新状态重放。
			e.commitMu.Unlock()
			continue
		}

		committed := false
		idx := -1
		var commitErr error
		if top.Status == StatusCompleted {
			if e.invariant != nil {
				// 先在“提交后状态”上评估不变量；不通过则整体放弃提交。
				prospective := cloneState(snapshot)
				applyPlan(prospective, top.WritePlan)
				if ok, violated := e.invariant(prospective); !ok {
					commitErr = &InvariantError{Violated: violated}
					top.Status = StatusPostFailed
					top.Err = commitErr.Error()
				}
			}
			if commitErr == nil {
				e.store.Commit(top.WritePlan)
				e.version++
				idx = e.version - 1
				e.order = append(e.order, chainID)
				committed = true
			}
		}
		e.commitMu.Unlock()

		rec := &ChainRecord{
			Entry:       entry,
			Input:       input,
			Frames:      x.frames,
			Status:      top.Status,
			Committed:   committed,
			CommitIndex: idx,
			StartedAt:   started,
			FinishedAt:  time.Now(),
		}
		if commitErr != nil {
			return &ChainResult{Record: rec, CommitOrder: -1, Replays: replay}, commitErr
		}
		return &ChainResult{Record: rec, CommitOrder: idx, Replays: replay}, nil
	}
}

// InvariantError 表示链条写入破坏了共享对象不变量，提交被整体拒绝。
type InvariantError struct{ Violated []string }

func (e *InvariantError) Error() string {
	return fmt.Sprintf("invariant violated: %v", e.Violated)
}

// readState 在提交临界区内原子读取“持久化快照 + 版本号”，
// 避免与提交推进产生数据竞争。
func (e *Engine) readState() (map[string]map[string]any, int) {
	e.commitMu.Lock()
	defer e.commitMu.Unlock()
	// commitMu 保护 version；store 自身锁保护 objects。锁顺序恒为 commitMu -> store。
	return e.store.Snapshot(), e.version
}

// State 返回当前持久化状态快照。
func (e *Engine) State() map[string]map[string]any {
	state, _ := e.readState()
	return state
}

// Version 返回当前持久化状态版本号（已提交链条数）。
func (e *Engine) Version() int {
	e.commitMu.Lock()
	defer e.commitMu.Unlock()
	return e.version
}

// CommitOrder 返回已提交链条 ID 的全序副本。
func (e *Engine) CommitOrder() []string {
	e.commitMu.Lock()
	defer e.commitMu.Unlock()
	out := make([]string, len(e.order))
	copy(out, e.order)
	return out
}

// RunNaiveSerial 是测试与审计用的朴素串行参考实现：
// 严格按给定顺序在同一存储上一条接一条执行并提交（无并发、无重放）。
// 并发引擎在同一提交顺序下的最终共享对象状态必须与本函数结果一致。
func RunNaiveSerial(reg *Registry, store *ObjectStore, inv Invariant,
	jobs []SerialJob) ([]*ChainResult, map[string]map[string]any) {
	eng := NewEngine(reg, store, inv)
	results := make([]*ChainResult, 0, len(jobs))
	for _, j := range jobs {
		res, err := eng.Run(context.Background(), j.ID, j.Entry, j.Input)
		_ = err // 失败链条在参考结果中同样以 Status 暴露
		results = append(results, res)
	}
	return results, store.Snapshot()
}

// SerialJob 是朴素串行参考实现中的一条链条作业。
type SerialJob struct {
	ID    string
	Entry string
	Input Params
}
