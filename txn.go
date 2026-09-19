package ontology

import "fmt"

// undoEntry 是日志中的一条逆序补偿记录。
type undoEntry struct {
	desc string
	undo func() error
}

// hookViolation 记录一个越权写入的钩子。
type hookViolation struct {
	action string
	index  int
}

// Txn 是一次 Action 执行所处的事务上下文（嵌套时共享根）。
type Txn struct {
	store *Store
	root  *Txn

	action       string
	allowedTypes map[string]struct{}

	// 以下字段只存在于根事务上。
	undo     []undoEntry
	impacts  []ImpactItem
	readOnly bool

	// fatalViolation 是根事务级别的“致命越权”标记：
	// 任何嵌套层级的钩子越权都会记录于此，即使外层吞掉内层错误，
	// 顶层提交时仍会失败并整体回滚。
	fatalViolation *hookViolation

	// hookIndex 是当前正在执行的钩子序号（每个 Txn 独立，从 1 开始）。
	hookIndex int

	// failUndoDesc 用于测试注入：撤销描述匹配该值的步骤会失败。
	failUndoDesc string

	engine *Engine
	chain  []string
	depth  int
}

func newRootTxn(st *Store, action string, types []string) *Txn {
	t := &Txn{store: st, action: action}
	t.root = t
	t.allowedTypes = typeSet(types)
	return t
}

func (t *Txn) child(action string, types []string) *Txn {
	return &Txn{
		store:        t.store,
		root:         t.root,
		action:       action,
		allowedTypes: typeSet(types),
	}
}

func typeSet(types []string) map[string]struct{} {
	m := make(map[string]struct{}, len(types))
	for _, ty := range types {
		m[ty] = struct{}{}
	}
	return m
}

// savepoint 记录当前日志长度，供内层回滚。
func (t *Txn) savepoint() int { return len(t.root.undo) }

func (t *Txn) impactPoint() int { return len(t.root.impacts) }

func (t *Txn) log(e undoEntry) { t.root.undo = append(t.root.undo, e) }

func (t *Txn) addImpact(i ImpactItem) { t.root.impacts = append(t.root.impacts, i) }

// beginHook 进入某个钩子的只读段。
func (t *Txn) beginHook() { t.root.readOnly = true }

// endHook 退出只读段。
func (t *Txn) endHook() { t.root.readOnly = false }

// noteViolation 记录越权写入（只记第一个，全部钩子仍继续执行）。
func (t *Txn) noteViolation(index int) {
	if t.root.fatalViolation == nil {
		t.root.fatalViolation = &hookViolation{action: t.action, index: index}
	}
}

// currentHook 返回当前 Txn 正在执行的钩子序号。
func (t *Txn) currentHook() int { return t.hookIndex }

// fatalViolationErr 读取当前越权标记（不移除，提交前统一判定）。
func (t *Txn) fatalViolationErr() *HookWriteError {
	v := t.root.fatalViolation
	if v == nil {
		return nil
	}
	return &HookWriteError{Action: v.action, Index: v.index}
}

// rollbackTo 逆序撤销 savepoint 之后的全部步骤。
// 返回非空表示撤销本身失败，此时存储已被标记为不一致。
func (t *Txn) rollbackTo(sp, ip int, cause error) error {
	r := t.root
	for i := len(r.undo) - 1; i >= sp; i-- {
		e := r.undo[i]
		var err error
		if e.desc == t.root.failUndoDesc {
			err = fmt.Errorf("模拟撤销失败")
		} else {
			err = e.undo()
		}
		if err != nil {
			step := fmt.Sprintf("%s: %v", e.desc, err)
			t.store.markInconsistent(step)
			return &RollbackError{Cause: cause, FailedStep: step}
		}
	}
	r.undo = r.undo[:sp]
	r.impacts = r.impacts[:ip]
	return nil
}

// RollbackError 表示回滚过程中有步骤无法撤销。
type RollbackError struct {
	Cause      error
	FailedStep string
}

func (e *RollbackError) Error() string {
	return fmt.Sprintf("回滚失败，存储标记为不一致；无法撤销: %s（原始原因: %v）", e.FailedStep, e.Cause)
}

func (t *Txn) rejectWrite() bool {
	if t.root.readOnly {
		return true
	}
	return false
}
