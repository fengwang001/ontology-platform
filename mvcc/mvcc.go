// Package mvcc 实现带子事务与命令号的元组版本可见性判定器。
//
// 事务组织为树：Begin 开始顶层事务，BeginSub 在运行中的事务下开始子事务。
// 事务状态有四种：运行中、已子提交（仅子事务）、已提交（仅顶层）、已中止。
// 由事务状态推导出的有效状态用于可见性判定：
//   - 自身已中止 => 中止
//   - 自身已子提交或已提交，且其根（顶层祖先）已提交 => 提交
//   - 其余 => 活动
//
// 可见性规则：事务 y 带命令号 cy 的效果对观察者（事务 x、命令号 c、快照 s）
// “被看到”，当且仅当：
//   - y 与 x 同根：y 的有效状态不是「中止」且 cy < c（严格小于）；
//   - y 与 x 不同根：y 的有效状态是「提交」且 y 的根在快照 s 的集合内。
//
// 元组 t 可见当且仅当其 xmin 的效果被看到且 xmax 的效果未被看到。
package mvcc

import (
	"errors"
	"sync"
)

// 各类拒绝原因，彼此可区分，可用 errors.Is 判定。
var (
	ErrTxNotPositive        = errors.New("mvcc: 事务号必须为正整数")
	ErrTxExists             = errors.New("mvcc: 事务号已存在")
	ErrTxNotFound           = errors.New("mvcc: 事务不存在")
	ErrTxNotRunning         = errors.New("mvcc: 事务非运行中")
	ErrTxTypeMismatch       = errors.New("mvcc: 提交方式与事务类型不符")
	ErrTxRunningDescendants = errors.New("mvcc: 存在运行中的后代事务")
	ErrNegativeCommand      = errors.New("mvcc: 命令号为负")
	ErrCommandOrder         = errors.New("mvcc: 命令号小于该事务树已用最大命令号")
	ErrTupleExists          = errors.New("mvcc: 元组已存在")
	ErrTupleNotFound        = errors.New("mvcc: 元组不存在")
	ErrTupleAlreadyDeleted  = errors.New("mvcc: 元组已有非中止的删除标记者")
	ErrSnapshotUnknown      = errors.New("mvcc: 快照编号未知")
)

// TxStatus 是事务的登记状态。
type TxStatus int

const (
	StatusRunning TxStatus = iota
	StatusSubCommitted
	StatusCommitted
	StatusAborted
)

// EffectiveStatus 是由事务树推导出的有效状态。
type EffectiveStatus int

const (
	EffectiveAborted EffectiveStatus = iota
	EffectiveCommitted
	EffectiveActive
)

type tx struct {
	id       int
	parent   *tx
	children []*tx
	status   TxStatus
	maxCmd   int // 仅根事务使用：该树已用最大命令号
}

type tupleVersion struct {
	xmin, cmin int
	xmax, cmax int
	hasXmax    bool
}

// Engine 是可见性判定器，所有方法可并发调用。
type Engine struct {
	mu        sync.Mutex
	txs       map[int]*tx
	tuples    map[string]*tupleVersion
	snapshots map[int]map[int]bool
	nextSnap  int
}

// New 创建一个空的判定器。
func New() *Engine {
	return &Engine{
		txs:       make(map[int]*tx),
		tuples:    make(map[string]*tupleVersion),
		snapshots: make(map[int]map[int]bool),
		nextSnap:  1,
	}
}

// root 返回事务所在树的根（顶层祖先）。
func (t *tx) root() *tx {
	for t.parent != nil {
		t = t.parent
	}
	return t
}

// effective 返回事务的有效状态。
func (t *tx) effective() EffectiveStatus {
	if t.status == StatusAborted {
		return EffectiveAborted
	}
	if (t.status == StatusSubCommitted || t.status == StatusCommitted) &&
		t.root().status == StatusCommitted {
		return EffectiveCommitted
	}
	return EffectiveActive
}

// hasRunningDescendant 报告事务是否存在运行中的后代。
func (t *tx) hasRunningDescendant() bool {
	for _, ch := range t.children {
		if ch.status == StatusRunning || ch.hasRunningDescendant() {
			return true
		}
	}
	return false
}

// abortTree 把事务及其全部后代置为已中止。
func (t *tx) abortTree() {
	t.status = StatusAborted
	for _, ch := range t.children {
		ch.abortTree()
	}
}

// Begin 开始顶层事务 x。
func (e *Engine) Begin(x int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if x <= 0 {
		return ErrTxNotPositive
	}
	if _, ok := e.txs[x]; ok {
		return ErrTxExists
	}
	e.txs[x] = &tx{id: x, status: StatusRunning}
	return nil
}

// BeginSub 在运行中的事务 p 之下开始子事务 x。
func (e *Engine) BeginSub(x, p int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if x <= 0 {
		return ErrTxNotPositive
	}
	if _, ok := e.txs[x]; ok {
		return ErrTxExists
	}
	parent, ok := e.txs[p]
	if !ok {
		return ErrTxNotFound
	}
	if parent.status != StatusRunning {
		return ErrTxNotRunning
	}
	sub := &tx{id: x, parent: parent, status: StatusRunning}
	parent.children = append(parent.children, sub)
	e.txs[x] = sub
	return nil
}

// commitPrelude 做两种提交共用的前三项检查，返回事务与错误。
func (e *Engine) commitPrelude(x int) (*tx, error) {
	t, ok := e.txs[x]
	if !ok {
		return nil, ErrTxNotFound
	}
	if t.status != StatusRunning {
		return nil, ErrTxNotRunning
	}
	return t, nil
}

// CommitSub 子提交运行中的子事务 x。
func (e *Engine) CommitSub(x int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, err := e.commitPrelude(x)
	if err != nil {
		return err
	}
	if t.parent == nil {
		return ErrTxTypeMismatch
	}
	if t.hasRunningDescendant() {
		return ErrTxRunningDescendants
	}
	t.status = StatusSubCommitted
	return nil
}

// Commit 提交运行中的顶层事务 x。
func (e *Engine) Commit(x int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, err := e.commitPrelude(x)
	if err != nil {
		return err
	}
	if t.parent != nil {
		return ErrTxTypeMismatch
	}
	if t.hasRunningDescendant() {
		return ErrTxRunningDescendants
	}
	t.status = StatusCommitted
	return nil
}

// Abort 中止运行中的事务 x 及其全部后代。
func (e *Engine) Abort(x int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, err := e.commitPrelude(x)
	if err != nil {
		return err
	}
	t.abortTree()
	return nil
}

// Snapshot 记录当前已提交顶层事务集合，返回从 1 起递增的快照编号。
func (e *Engine) Snapshot() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	committed := make(map[int]bool)
	for _, t := range e.txs {
		if t.parent == nil && t.status == StatusCommitted {
			committed[t.id] = true
		}
	}
	id := e.nextSnap
	e.nextSnap++
	e.snapshots[id] = committed
	return id
}

// writePrelude 做 Insert/Delete 共用的前四项检查，返回写事务与其根。
func (e *Engine) writePrelude(x, c int) (*tx, *tx, error) {
	t, ok := e.txs[x]
	if !ok {
		return nil, nil, ErrTxNotFound
	}
	if t.status != StatusRunning {
		return nil, nil, ErrTxNotRunning
	}
	if c < 0 {
		return nil, nil, ErrNegativeCommand
	}
	root := t.root()
	if c < root.maxCmd {
		return nil, nil, ErrCommandOrder
	}
	return t, root, nil
}

// Insert 为元组 t 建立版本，xmin=x、cmin=c。
func (e *Engine) Insert(t string, x, c int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, root, err := e.writePrelude(x, c)
	if err != nil {
		return err
	}
	if _, ok := e.tuples[t]; ok {
		return ErrTupleExists
	}
	e.tuples[t] = &tupleVersion{xmin: x, cmin: c}
	root.maxCmd = c
	return nil
}

// Delete 给元组 t 打删除标记，xmax=x、cmax=c。
func (e *Engine) Delete(t string, x, c int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, root, err := e.writePrelude(x, c)
	if err != nil {
		return err
	}
	tv, ok := e.tuples[t]
	if !ok {
		return ErrTupleNotFound
	}
	if tv.hasXmax {
		marker := e.txs[tv.xmax]
		if marker.effective() != EffectiveAborted {
			return ErrTupleAlreadyDeleted
		}
	}
	tv.xmax, tv.cmax, tv.hasXmax = x, c, true
	root.maxCmd = c
	return nil
}

// seen 判定事务 y 带命令号 cy 的效果是否被观察者看到。
// 调用方须持有锁；observerRoot 为观察者事务的根。
func (e *Engine) seen(observerRoot *tx, snap map[int]bool, y, cy, c int) bool {
	ty := e.txs[y]
	if ty.root() == observerRoot {
		return ty.effective() != EffectiveAborted && cy < c
	}
	return ty.effective() == EffectiveCommitted && snap[ty.root().id]
}

// Visible 判定观察者（运行中事务 x、当前命令号 c、快照 s）能否看到元组 t。
func (e *Engine) Visible(t string, x, c, s int) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	observer, ok := e.txs[x]
	if !ok {
		return false, ErrTxNotFound
	}
	if observer.status != StatusRunning {
		return false, ErrTxNotRunning
	}
	if c < 0 {
		return false, ErrNegativeCommand
	}
	snap, ok := e.snapshots[s]
	if !ok {
		return false, ErrSnapshotUnknown
	}
	tv, ok := e.tuples[t]
	if !ok {
		return false, ErrTupleNotFound
	}
	root := observer.root()
	if !e.seen(root, snap, tv.xmin, tv.cmin, c) {
		return false, nil
	}
	if tv.hasXmax && e.seen(root, snap, tv.xmax, tv.cmax, c) {
		return false, nil
	}
	return true, nil
}
