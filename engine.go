package ontology

import "fmt"

// MaxDepth 是允许的最大嵌套深度（根 Action 深度为 1）。
const MaxDepth = 16

// Engine 注册 ActionType 并执行事务。
type Engine struct {
	store   *Store
	log     *Log
	actions map[string]*ActionType

	// failUndoDesc 仅供测试：撤销步骤描述与之相等时模拟撤销失败。
	failUndoDesc string
}

// NewEngine 创建引擎。
func NewEngine(st *Store) *Engine {
	return &Engine{
		store:   st,
		log:     NewLog(),
		actions: make(map[string]*ActionType),
	}
}

// Register 注册一个 ActionType。
func (e *Engine) Register(a *ActionType) {
	e.actions[a.Name] = a
}

// Store / Log 暴露存储与执行日志。
func (e *Engine) Store() *Store { return e.store }
func (e *Engine) Log() *Log     { return e.log }

// Execute 从调用方视角执行一个顶层 Action，整个过程持有存储锁。
func (e *Engine) Execute(name string, args map[string]any) (*Record, error) {
	a, ok := e.actions[name]
	if !ok {
		return nil, fmt.Errorf("未注册的 ActionType %q", name)
	}
	e.store.Lock()
	defer e.store.Unlock()

	params, err := normalize(a, args)
	if err != nil {
		return nil, err
	}

	txn := newRootTxn(e.store, name, a.ObjectTypes)
	txn.root.failUndoDesc = e.failUndoDesc
	// 根 Action 之前没有调用链；runAction 会把当前名字追加进链。
	if err := e.runAction(txn, a, params, nil, 1); err != nil {
		return nil, err
	}

	// 顶层提交前最后检查越权写入（任何嵌套层级的越权都会在此被发现）。
	if hw := txn.fatalViolationErr(); hw != nil {
		if rbErr := txn.rollbackTo(0, 0, hw); rbErr != nil {
			return nil, rbErr
		}
		return nil, hw
	}

	return e.log.append(name, params, append([]ImpactItem(nil), txn.root.impacts...)), nil
}

// runAction 在给定事务内运行 Action；嵌套时通过 t.Call 进入。
func (e *Engine) runAction(t *Txn, a *ActionType, params map[string]any,
	chain []string, depth int) error {
	// 递归检测。
	next := append(append([]string(nil), chain...), a.Name)
	for i, n := range chain {
		if n == a.Name {
			// 报告从该 Action 第一次出现到再次出现的完整环链。
			return &RecursionError{Chain: append([]string(nil), next[i:]...)}
		}
	}
	// 深度上限。
	if depth > MaxDepth {
		return &DepthError{Chain: next, Limit: MaxDepth}
	}

	child := t.child(a.Name, a.ObjectTypes)

	// 有序执行全部前置钩子：拒绝与越权都会收集，钩子继续跑完。
	var reject *HookRejectError
	for i, h := range a.Hooks {
		child.hookIndex = i + 1
		child.beginHook()
		reason := h(child, params)
		child.endHook()
		if reason != "" && reject == nil {
			reject = &HookRejectError{Action: a.Name, Index: i + 1, Reason: reason}
		}
	}

	sp, ip := t.savepoint(), t.impactPoint()

	if reject != nil {
		// 钩子写入已被吞掉，不会真正改状态；直接返回拒绝。
		// 若存在越权，越权优先并保证顶层整体失败。
		if hw := child.fatalViolationErr(); hw != nil {
			return hw
		}
		return reject
	}

	// 把引擎注入子事务，供业务处理器嵌套调用。
	child.engine = e
	child.chain = append(append([]string(nil), chain...), a.Name)
	child.depth = depth

	if err := a.Handler(child, params); err != nil {
		if rbErr := child.rollbackTo(sp, ip, err); rbErr != nil {
			return rbErr
		}
		return err
	}

	if hw := child.fatalViolationErr(); hw != nil {
		if rbErr := child.rollbackTo(sp, ip, hw); rbErr != nil {
			return rbErr
		}
		return hw
	}
	return nil
}

// Call 在业务处理器中嵌套调用另一个 Action（保存点语义）。
func (t *Txn) Call(name string, args map[string]any) error {
	return t.engine.callNested(t, name, args)
}

func (e *Engine) callNested(parent *Txn, name string, args map[string]any) error {
	a, ok := e.actions[name]
	if !ok {
		return fmt.Errorf("未注册的 ActionType %q", name)
	}
	params, err := normalize(a, args)
	if err != nil {
		return err
	}
	return e.runAction(parent, a, params, parent.chain, parent.depth+1)
}
