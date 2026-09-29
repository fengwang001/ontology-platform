// Package rename 提供可撤销的批量重命名执行器：把一组旧名到新名的映射
// 在同一命名空间中按「同时生效」的语义拆成单步改名执行，并支持整体撤销。
package rename

import (
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
)

// Pair 表示一条「旧名 -> 新名」的映射。
type Pair struct {
	Old string
	New string
}

// Step 表示一次单步改名。
type Step struct {
	Old string
	New string
}

// BatchID 标识一次成功提交的批次，用于撤销。
type BatchID uint64

// 可区分的拒绝 / 失败原因，均可用 errors.Is 判定。
var (
	// ErrEmptyName 名字为空（旧名或新名）。
	ErrEmptyName = errors.New("rename: empty name")
	// ErrDuplicateOld 同一旧名出现两次。
	ErrDuplicateOld = errors.New("rename: duplicate old name")
	// ErrConflictingNew 两个旧名映射到同一新名。
	ErrConflictingNew = errors.New("rename: two old names map to the same new name")
	// ErrOldNotFound 旧名不存在于命名空间。
	ErrOldNotFound = errors.New("rename: old name does not exist")
	// ErrNewNameExists 新名已存在且不在本批被搬走的旧名中。
	ErrNewNameExists = errors.New("rename: new name already exists and is not moved away")
	// ErrStepFailed 执行中某一步失败（已执行的步骤已按逆序撤回）。
	ErrStepFailed = errors.New("rename: step failed, executed steps rolled back")
	// ErrNothingToUndo 没有任何成功批次可撤销。
	ErrNothingToUndo = errors.New("rename: no successful batch to undo")
	// ErrStaleUndo 该批次之后已有新批次生效，拒绝撤销。
	ErrStaleUndo = errors.New("rename: a newer batch has been committed")
	// ErrAlreadyUndone 该批次已被撤销过。
	ErrAlreadyUndone = errors.New("rename: batch already undone")
)

// Namespace 是同一命名空间下名字的集合。
type Namespace struct {
	names map[string]struct{}
}

// NewNamespace 用给定名字构造命名空间。
func NewNamespace(names ...string) *Namespace {
	ns := &Namespace{names: make(map[string]struct{}, len(names))}
	for _, n := range names {
		ns.names[n] = struct{}{}
	}
	return ns
}

// Has 报告名字是否已被占用。
func (n *Namespace) Has(name string) bool {
	_, ok := n.names[name]
	return ok
}

// rename 执行单步改名，调用方须保证 old 存在且 new 不存在。
func (n *Namespace) rename(old, new string) {
	delete(n.names, old)
	n.names[new] = struct{}{}
}

// snapshot 返回名字的排序副本。
func (n *Namespace) snapshot() []string {
	out := make([]string, 0, len(n.names))
	for name := range n.names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// record 记录最近一次成功批次，用于撤销。
type record struct {
	id     BatchID
	steps  []Step
	undone bool
}

// Executor 在命名空间上串行执行批量重命名，并支持撤销最近一次成功批次。
type Executor struct {
	mu     sync.RWMutex
	ns     *Namespace
	last   *record
	nextID BatchID
	log    *log.Logger
}

// NewExecutor 构造执行器；logger 可为 nil（不打印日志）。
func NewExecutor(ns *Namespace, logger *log.Logger) *Executor {
	return &Executor{ns: ns, log: logger}
}

// Snapshot 返回当前命名空间的排序快照。
// 读操作与批次互斥，读者只能看到某个批次之前或之后的完整命名空间。
func (e *Executor) Snapshot() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.ns.snapshot()
}

// Execute 校验并规划映射，然后串行执行单步改名。
// 校验失败整体拒绝且不改任何名字；执行中某一步失败时按逆序撤回已执行步骤。
// 成功时返回批次 ID 与步骤序列。
func (e *Executor) Execute(pairs []Pair, tempPrefix string) (BatchID, []Step, error) {
	return e.execute(pairs, tempPrefix, -1)
}

// execute 中 failAt >= 0 时在第 failAt 步注入失败（仅用于测试）。
func (e *Executor) execute(pairs []Pair, tempPrefix string, failAt int) (BatchID, []Step, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	p, err := plan(pairs, tempPrefix, e.ns)
	if err != nil {
		e.logf("reject input=%v reason=%v", pairs, err)
		return 0, nil, err
	}
	e.logf("input=%v plan=%s", pairs, p)

	done := make([]Step, 0, len(p.steps))
	for i, s := range p.steps {
		if i == failAt {
			e.rollback(done)
			return 0, p.steps, fmt.Errorf("%w: injected failure at step %d (%q -> %q)", ErrStepFailed, i, s.Old, s.New)
		}
		if !e.ns.Has(s.Old) || e.ns.Has(s.New) {
			e.rollback(done)
			return 0, p.steps, fmt.Errorf("%w: step %d (%q -> %q) violates invariant", ErrStepFailed, i, s.Old, s.New)
		}
		e.ns.rename(s.Old, s.New)
		done = append(done, s)
	}

	e.nextID++
	e.last = &record{id: e.nextID, steps: p.steps}
	e.logf("committed batch=%d steps=%v", e.nextID, p.steps)
	return e.nextID, p.steps, nil
}

// rollback 按逆序撤回已执行的步骤。
func (e *Executor) rollback(done []Step) {
	for i := len(done) - 1; i >= 0; i-- {
		e.ns.rename(done[i].New, done[i].Old)
	}
	if len(done) > 0 {
		e.logf("rolled back %d steps", len(done))
	}
}

// Undo 撤销指定批次。仅当它是最近一次成功批次、且未被撤销过时生效；
// 其后已有新批次或重复撤销都会被拒绝。
func (e *Executor) Undo(id BatchID) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.last == nil {
		return ErrNothingToUndo
	}
	if e.last.id != id {
		return fmt.Errorf("%w: batch %d is not the latest (latest is %d)", ErrStaleUndo, id, e.last.id)
	}
	if e.last.undone {
		return fmt.Errorf("%w: batch %d", ErrAlreadyUndone, id)
	}
	for i := len(e.last.steps) - 1; i >= 0; i-- {
		s := e.last.steps[i]
		if !e.ns.Has(s.New) || e.ns.Has(s.Old) {
			return fmt.Errorf("rename: undo of batch %d blocked at step %d (%q -> %q)", id, i, s.Old, s.New)
		}
		e.ns.rename(s.New, s.Old)
	}
	e.last.undone = true
	e.logf("undone batch=%d", id)
	return nil
}

func (e *Executor) logf(format string, args ...any) {
	if e.log != nil {
		e.log.Printf(format, args...)
	}
}
