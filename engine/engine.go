// Package engine 是动作执行引擎：解释动作定义、编排嵌套调用，
// 协作事务边界管理（tx）、钩子调度（hooks）与错误归一化（errs）三个模块，
// 并通过基于序号的轮转调度保证并发最外层动作的可串行化与重放确定性。
package engine

import (
	"errors"
	"fmt"
	"sync"

	"ontology/hooks"
	"ontology/spec"
	"ontology/tx"
)

// MaxDepth 是嵌套调用的最大深度保护（自调用动作必须受此限制）。
const MaxDepth = 64

// ErrDepthLimit 是嵌套深度超过 MaxDepth 的保护性错误，
// 属于引擎防护而非动作拒绝，因此不归一化为三类错误之一。
var ErrDepthLimit = errors.New("engine: nesting depth limit exceeded")

// Engine 是动作执行引擎。
//
// 并发模型：所有最外层动作调用按序号（turn）严格逐个执行，
// 同一时刻只有一个事务在运行。这保证了：
//   - 可串行化：并发调用的最终效果等价于按序号顺序逐一应用；
//   - 不交错：一个最外层动作（含其全部嵌套写入）整体占据
//     串行顺序中的一个位置；
//   - 重放确定性：相同的调用序号序列必然产生完全相同的
//     最终状态与钩子触发记录。
type Engine struct {
	store *tx.Store
	reg   *hooks.Registry
	disp  *hooks.Dispatcher
	defs  map[string]spec.ActionDef

	mu       sync.Mutex
	cond     *sync.Cond
	nextTurn uint64 // 下一个允许执行的序号
	claimed  uint64 // 下一个自动分配的序号（Execute 使用）
	txSeq    uint64 // 已执行事务数，兼作事务 ID 来源
	hookLog  []hooks.Record
}

// New 创建空引擎。
func New() *Engine {
	e := &Engine{
		store: tx.NewStore(),
		reg:   hooks.NewRegistry(),
		defs:  make(map[string]spec.ActionDef),
	}
	e.disp = hooks.NewDispatcher(e.reg)
	e.cond = sync.NewCond(&e.mu)
	return e
}

// RegisterType 注册对象类型及其字段模式。
func (e *Engine) RegisterType(name string, schema spec.Schema) {
	e.store.RegisterType(name, schema)
}

// RegisterAction 注册动作定义。被嵌套调用的动作必须先注册，
// 以便在注册期发现悬空引用。
func (e *Engine) RegisterAction(def spec.ActionDef) error {
	if def.Name == "" {
		return errors.New("engine: action name must not be empty")
	}
	for _, op := range def.Body {
		if op.Write != nil && op.Call != "" {
			return fmt.Errorf("engine: action %q has a step that is both write and call", def.Name)
		}
		if op.Write == nil && op.Call == "" {
			return fmt.Errorf("engine: action %q has an empty step", def.Name)
		}
		if op.Call != "" {
			if _, ok := e.defs[op.Call]; !ok && op.Call != def.Name {
				return fmt.Errorf("engine: action %q calls unknown action %q", def.Name, op.Call)
			}
		}
	}
	e.defs[def.Name] = def
	return nil
}

// RegisterPreHook 为对象类型注册前置校验钩子。
func (e *Engine) RegisterPreHook(objectType, name string, fn hooks.Func) {
	e.reg.RegisterPre(objectType, name, fn)
}

// RegisterPostHook 为对象类型注册后置校验钩子。
func (e *Engine) RegisterPostHook(objectType, name string, fn hooks.Func) {
	e.reg.RegisterPost(objectType, name, fn)
}

// Execute 以自动分配的序号发起一个最外层动作调用。
// 单线程调用者按调用顺序依次执行；与 ExecuteWithSeq 不要混用。
func (e *Engine) Execute(action string) error {
	e.mu.Lock()
	seq := e.claimed
	e.claimed++
	e.mu.Unlock()
	return e.ExecuteWithSeq(seq, action)
}

// ExecuteWithSeq 以显式序号发起一个最外层动作调用：
// 无论多少个 goroutine 并发提交，引擎严格按序号从小到大
// 逐个执行。序号必须从引擎当前轮转位置开始连续，否则
// 靠后的调用会一直等待缺口被补齐。
func (e *Engine) ExecuteWithSeq(seq uint64, action string) error {
	e.mu.Lock()
	for e.nextTurn != seq {
		e.cond.Wait()
	}
	e.mu.Unlock()

	err := e.execute(action)

	e.mu.Lock()
	e.nextTurn++
	e.cond.Broadcast()
	e.mu.Unlock()
	return err
}

// execute 在持有执行权（轮转序号）的情况下运行一个最外层动作。
func (e *Engine) execute(action string) error {
	e.txSeq++
	txID := e.txSeq
	t := e.store.Begin(txID)
	rec := hooks.NewRecorder()

	err := e.runAction(t, action, nil, 0, rec)
	if err == nil {
		// 后置钩子：最外层全部写入应用之后、提交之前触发一次。
		err = e.disp.FirePost(t.AffectedTypes(), hooks.Context{
			Kind:       hooks.Post,
			State:      t.View(),
			TxID:       txID,
			Depth:      0,
			ActionPath: []string{action},
		}, rec)
	}
	if err != nil {
		t.Rollback()
		e.appendHookLog(rec.Flush("rolled-back"))
		return err
	}
	t.Commit()
	e.appendHookLog(rec.Flush("committed"))
	return nil
}

// runAction 递归执行动作体：写入与嵌套调用按定义顺序解释。
// 嵌套调用共享同一个事务边界（同一个 *tx.Tx）。
func (e *Engine) runAction(t *tx.Tx, name string, parentPath []string, depth int, rec *hooks.Recorder) error {
	if depth > MaxDepth {
		return ErrDepthLimit
	}
	def, ok := e.defs[name]
	if !ok {
		return fmt.Errorf("engine: unknown action %q", name)
	}
	path := append(append([]string(nil), parentPath...), name)
	for _, op := range def.Body {
		if op.Write != nil {
			w := *op.Write
			// 1. 参数校验（优先级最高的拒绝原因）。
			if err := t.Validate(w, path); err != nil {
				return err
			}
			// 2. 前置钩子：看到写入生效前、含外层未提交写入的状态。
			if err := e.disp.FirePre(hooks.Context{
				Kind:       hooks.Pre,
				ObjectType: w.Type,
				Write:      &w,
				State:      t.View(),
				TxID:       t.ID(),
				Depth:      depth,
				ActionPath: path,
			}, rec); err != nil {
				return err
			}
			// 3. 应用写入到共享工作副本。
			t.Apply(w)
			continue
		}
		// 嵌套调用：并入同一事务边界。
		if err := e.runAction(t, op.Call, path, depth+1, rec); err != nil {
			return err
		}
	}
	return nil
}

// HookLog 返回全局钩子触发记录（按事务执行顺序）。
func (e *Engine) HookLog() []hooks.Record {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]hooks.Record(nil), e.hookLog...)
}

// State 返回已提交状态的只读视图。
func (e *Engine) State() hooks.StateView { return e.store.Committed() }

// StoreStats 返回存储层开销统计（用于 O(1) 快照开销的验证）。
func (e *Engine) StoreStats() tx.Stats { return e.store.Stats() }

// Store 返回底层事务存储（供测试与基准直接测量视图构造开销）。
func (e *Engine) Store() *tx.Store { return e.store }

func (e *Engine) appendHookLog(records []hooks.Record) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.hookLog = append(e.hookLog, records...)
}
