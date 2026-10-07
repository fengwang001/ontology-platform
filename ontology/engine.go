package ontology

import (
	"encoding/json"
	"fmt"
	"sync"
)

// Engine 在 Registry 与 Store 之上执行（可能嵌套的）动作调用链条。
type Engine struct {
	reg   *Registry
	store *Store

	mu        sync.Mutex
	chainSeq  uint64
	commitLog []CommitRecord
}

// CommitRecord 记录一次成功提交在全序中的位置。
type CommitRecord struct {
	Seq     uint64
	ChainID uint64
	Action  string
	ArgsKey string
}

func NewEngine(reg *Registry, store *Store) (*Engine, error) {
	if err := reg.Validate(); err != nil {
		return nil, err
	}
	return &Engine{reg: reg, store: store}, nil
}

// Execute 执行一条以 action 为根的调用链条。
//
// 链条在快照上求值、在本地累积写入计划，提交时在存储锁内做读集冲突检测，
// 冲突则整体重试；因此所有已提交链条的效果等价于按提交序号串行执行。
func (e *Engine) Execute(action string, args Args) *ChainResult {
	e.mu.Lock()
	e.chainSeq++
	chainID := e.chainSeq
	e.mu.Unlock()

	for {
		snapshot := e.store.Snapshot()
		chain := &chainExec{
			engine:    e,
			chainID:   chainID,
			snapshot:  snapshot,
			reads:     map[string]uint64{},
			plan:      newWritePlan(),
			trace:     &Trace{},
			ancestors: map[string]struct{}{},
		}
		key := argsKey(action, args)
		root := &Context{
			chain:   chain,
			args:    args,
			outputs: map[string]any{},
			depth:   0,
			path:    action,
		}
		root.plan = chain.plan
		root.view = &View{base: snapshot, layers: []*WritePlan{chain.plan}, reads: chain.reads}

		status, _, reason, outputs := chain.runAction(root, action, args, true)
		if status == runAborted {
			chain.trace.conclusion = fmt.Sprintf("aborted: %s", reason)
			return &ChainResult{ChainID: chainID, Aborted: true, Reason: reason, Trace: chain.trace}
		}

		seq, conflict, err := e.store.commit(chain.reads, chain.plan)
		if conflict {
			continue
		}
		if err != nil {
			reason := fmt.Sprintf("invariant violated: %v", err)
			chain.trace.conclusion = "aborted: " + reason
			return &ChainResult{ChainID: chainID, Aborted: true, Reason: reason, Trace: chain.trace}
		}
		chain.trace.committed = true
		chain.trace.conclusion = fmt.Sprintf("committed at seq %d", seq)
		e.mu.Lock()
		e.commitLog = append(e.commitLog, CommitRecord{Seq: seq, ChainID: chainID, Action: action, ArgsKey: key})
		e.mu.Unlock()
		return &ChainResult{
			ChainID:   chainID,
			Committed: true,
			CommitSeq: seq,
			Outputs:   outputs,
			Trace:     chain.trace,
		}
	}
}

// CommitLog 返回已提交链条的全序记录（按提交序号升序）。
func (e *Engine) CommitLog() []CommitRecord {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]CommitRecord, len(e.commitLog))
	copy(out, e.commitLog)
	return out
}

// ChainResult 是一条调用链条的最终结果。
type ChainResult struct {
	ChainID   uint64
	Committed bool
	Aborted   bool
	Reason    string
	CommitSeq uint64
	Outputs   map[string]any
	Trace     *Trace
}

// Context 是动作 Run 函数的执行上下文。
type Context struct {
	chain   *chainExec
	action  *Action
	args    Args
	view    *View
	plan    *WritePlan
	outputs map[string]any
	depth   int
	path    string
}

func (c *Context) Arg(name string) any { return c.args[name] }

// Get 读取对象：可见外层尚未提交的写入计划，再回落到持久化快照。
func (c *Context) Get(id string) (Object, bool) { return c.view.Get(id) }

// Put 把一次写入加入当前层的写入计划（尚未提交）。
func (c *Context) Put(obj Object) { c.plan.put(obj) }

// Delete 把一次删除加入当前层的写入计划（尚未提交）。
func (c *Context) Delete(id string) { c.plan.del(id) }

func (c *Context) SetOutput(name string, v any) { c.outputs[name] = v }

// Invoke 触发一次嵌套调用。
//
// 处理顺序固定为：自我触发守卫 -> 声明一致性 -> 前置条件 -> 执行 -> 后置条件。
// 守卫在任何前置条件求值之前完成；关键调用失败通过整体放弃传播。
func (c *Context) Invoke(action string, args Args, critical bool) *InvokeResult {
	chain := c.chain
	key := argsKey(action, args)
	path := c.path + "/" + action

	// 1. 自我触发守卫：仅查当前链条路径上的祖先集合，O(1) 一次查表。
	chain.trace.guardChecks++
	if _, found := chain.ancestors[key]; found {
		chain.trace.add(Event{
			Path: path, Depth: c.depth + 1, Action: action, ArgsKey: key,
			Critical: critical, Phase: PhaseGuard,
			Outcome: OutcomeSelfTriggerRejected,
			Detail:  "same (action, args) already on the call path; rejected before precondition evaluation",
		})
		return &InvokeResult{Outcome: OutcomeSelfTriggerRejected,
			Err: fmt.Errorf("self-trigger rejected: %s", key)}
	}

	// 2. 声明一致性：动态调用必须匹配静态声明。
	if !c.action.declares(action, critical) {
		panic(&abortError{reason: fmt.Sprintf(
			"declaration error: action %q performs undeclared call %q (critical=%v)",
			c.action.Name, action, critical)})
	}

	child, ok := c.chain.childContext(c, action, args, path)
	if !ok {
		panic(&abortError{reason: fmt.Sprintf("unknown action %q", action)})
	}

	// 3-5. 前置 -> 执行 -> 后置；关键失败经 panic 向外层传播整体放弃。
	status, outcome, reason, outputs := chain.runAction(child, action, args, critical)
	switch status {
	case runAborted:
		panic(&abortError{reason: reason})
	case runFailedNonCritical:
		// 非关键失败：子计划直接丢弃，不并入外层，失败已被记录。
		return &InvokeResult{Outcome: outcome, Err: fmt.Errorf("%s", reason)}
	}

	// 成功：内层写入计划并入外层，后续流程可见。
	c.plan.merge(child.plan)
	return &InvokeResult{Outcome: OutcomeOK, Outputs: outputs}
}

// runStatus 是一层调用的执行结局。
type runStatus int

const (
	runOK runStatus = iota
	runFailedNonCritical
	runAborted
)

// abortError 是关键调用失败导致的整体放弃信号，经 panic  unwind 到链条根部。
type abortError struct{ reason string }

func (e *abortError) Error() string { return e.reason }

// childContext 为一次嵌套调用构造子上下文：视图叠加新的写入计划层。
func (chain *chainExec) childContext(parent *Context, action string, args Args, path string) (*Context, bool) {
	act, ok := chain.engine.reg.Get(action)
	if !ok {
		return nil, false
	}
	plan := newWritePlan()
	layers := make([]*WritePlan, 0, len(parent.view.layers)+1)
	layers = append(layers, parent.view.layers...)
	layers = append(layers, plan)
	child := &Context{
		chain:   chain,
		action:  act,
		args:    args,
		plan:    plan,
		outputs: map[string]any{},
		depth:   parent.depth + 1,
		path:    path,
	}
	child.view = &View{base: chain.snapshot, layers: layers, reads: chain.reads}
	return child, true
}

// runAction 执行一层调用：登记祖先键 -> 前置 -> Run -> 后置。
// 失败时按关键性决定整体放弃（aborted=true）或记录后继续。
func (chain *chainExec) runAction(ctx *Context, action string, args Args, critical bool) (status runStatus, outcome Outcome, reason string, outputs map[string]any) {
	act, ok := chain.engine.reg.Get(action)
	if !ok {
		return runAborted, OutcomeRunFailedCritical, fmt.Sprintf("unknown action %q", action), nil
	}
	ctx.action = act
	key := argsKey(action, args)
	chain.ancestors[key] = struct{}{}
	defer delete(chain.ancestors, key)

	defer func() {
		if r := recover(); r != nil {
			if ae, isAbort := r.(*abortError); isAbort {
				status, outcome, reason, outputs = runAborted, OutcomeRunFailedCritical, ae.reason, nil
				return
			}
			panic(r)
		}
	}()

	fail := func(phase Phase, oc Outcome, err error) (runStatus, Outcome, string, map[string]any) {
		chain.trace.add(Event{
			Path: ctx.path, Depth: ctx.depth, Action: action, ArgsKey: key,
			Critical: critical, Phase: phase,
			PreBasis: ctx.view.basis(), PostBasis: ctx.view.basis(),
			Outcome: oc, Detail: err.Error(),
		})
		msg := fmt.Sprintf("%s failed at %s: %v", ctx.path, phase, err)
		if critical {
			return runAborted, oc, msg, nil
		}
		return runFailedNonCritical, oc, msg, nil
	}

	preBasis := ctx.view.basis()
	for _, cond := range act.Preconditions {
		if err := cond.Check(ctx.view); err != nil {
			oc := OutcomeNonCriticalFailed
			if critical {
				oc = OutcomePreconditionFailed
			}
			return fail(PhasePre, oc, fmt.Errorf("precondition %q: %w", cond.Name, err))
		}
	}

	if err := act.Run(ctx); err != nil {
		oc := OutcomeNonCriticalFailed
		if critical {
			oc = OutcomeRunFailedCritical
		}
		return fail(PhaseRun, oc, err)
	}

	for _, cond := range act.Postconditions {
		if err := cond.Check(ctx.view); err != nil {
			oc := OutcomeNonCriticalFailed
			if critical {
				oc = OutcomePostconditionFailedCritical
			}
			return fail(PhasePost, oc, fmt.Errorf("postcondition %q: %w", cond.Name, err))
		}
	}

	chain.trace.add(Event{
		Path: ctx.path, Depth: ctx.depth, Action: action, ArgsKey: key,
		Critical: critical, Phase: PhaseRun,
		PreBasis: preBasis, PostBasis: ctx.view.basis(),
		Outcome: OutcomeOK,
	})
	return runOK, OutcomeOK, "", ctx.outputs
}

// argsKey 计算 (action, args) 组合的规范键，用于自我触发检测。
func argsKey(action string, args Args) string {
	raw, err := json.Marshal(args)
	if err != nil {
		raw = []byte(fmt.Sprintf("%v", args))
	}
	return action + "|" + string(raw)
}

// InvokeResult 是一次嵌套调用的返回。
type InvokeResult struct {
	Outcome Outcome
	Outputs map[string]any
	Err     error
}

func (r *InvokeResult) OK() bool { return r != nil && r.Outcome == OutcomeOK }

// chainExec 是一条调用链条的执行期状态。
type chainExec struct {
	engine    *Engine
	chainID   uint64
	snapshot  *Snapshot
	reads     map[string]uint64
	plan      *WritePlan
	trace     *Trace
	ancestors map[string]struct{}
}
