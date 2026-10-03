// Package hekaton 实现 Hekaton 风格的乐观多版本事务引擎。
//
// 读者在创建者进入预备态后即可推测性读取其写入，并对该创建者记录提交依赖；
// 依赖的事务提交时依赖者按事务号次序随之提交，依赖失败时级联中止全部依赖者。
package hekaton

import (
	"errors"
	"sort"
	"sync"
)

// 事务状态。
const (
	Active    = 0 // 活跃
	Prepared  = 1 // 预备（有结束时间 ET）
	Committed = 2 // 已提交
	Aborted   = 3 // 已中止
)

// 哨兵错误：被拒绝的调用不会改变任何引擎状态。
var (
	ErrInvalidK      = errors.New("hekaton: K must be in [1,64]")
	ErrUnknownTxn    = errors.New("hekaton: unknown transaction id")
	ErrInvalidState  = errors.New("hekaton: transaction is not in the required state")
	ErrKeyOutOfRange = errors.New("hekaton: key out of range")
)

// version 是某个键上的一个版本，同键版本自旧到新存放在切片中。
type version struct {
	value   int
	creator int // 创建者事务号；0 为虚拟已提交事务（ET=0）
	ender   int // 结束者事务号；0 表示无结束者
}

// txn 是一个事务的运行时记录。
type txn struct {
	id         int
	state      int
	rt         int
	et         int
	finished   bool             // 预备态下是否已调用 Finish
	deps       map[int]struct{} // 我依赖的、尚未终结的事务
	dependents map[int]struct{} // 依赖我的事务
}

// Engine 是乐观多版本事务引擎。所有方法均可被并发调用，
// 内部以单一互斥锁串行化，结果等价于某个合法的串行顺序。
type Engine struct {
	mu     sync.Mutex
	k      int
	clock  int
	nextID int
	txns   map[int]*txn
	keys   [][]*version // keys[i] 自旧到新存放键 i 的全部版本
}

// New 创建引擎：K 必须落在 [1,64]，否则整体拒绝。
// 每个键初始拥有一个已提交版本：值 0，创建者为虚拟已提交事务（ET=0）。
func New(k int) (*Engine, error) {
	if k < 1 || k > 64 {
		return nil, ErrInvalidK
	}
	e := &Engine{
		k:      k,
		nextID: 1,
		txns:   make(map[int]*txn),
		keys:   make([][]*version, k),
	}
	for i := range e.keys {
		e.keys[i] = []*version{{value: 0, creator: 0, ender: 0}}
	}
	return e, nil
}

// Begin 令全局时钟加一，新事务 RT=n，事务号从 1 起独立递增。
func (e *Engine) Begin() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.clock++
	id := e.nextID
	e.nextID++
	e.txns[id] = &txn{
		id:         id,
		state:      Active,
		rt:         e.clock,
		deps:       make(map[int]struct{}),
		dependents: make(map[int]struct{}),
	}
	return id
}

// creatorState 返回创建者状态与 ET；id==0 为虚拟已提交事务（ET=0）。
func (e *Engine) creatorState(id int) (state, et int) {
	if id == 0 {
		return Committed, 0
	}
	tr := e.txns[id]
	return tr.state, tr.et
}

// startVisible 判定版本起点（创建者）对读取者 r 是否可见。
func (e *Engine) startVisible(v *version, r *txn) bool {
	if v.creator == r.id {
		return true
	}
	state, et := e.creatorState(v.creator)
	switch state {
	case Active, Aborted:
		return false
	default: // Prepared 或 Committed
		return et < r.rt
	}
}

// endVisible 判定版本终点（结束者）对读取者 r 是否可见。
func (e *Engine) endVisible(v *version, r *txn) bool {
	if v.ender == 0 {
		return true
	}
	if v.ender == r.id {
		return false
	}
	er := e.txns[v.ender]
	switch er.state {
	case Active:
		return true
	case Aborted:
		return true // 结束者已中止视为无结束者
	default: // Prepared 或 Committed
		return r.rt < er.et
	}
}

// addDep 记录依赖：who 依赖 whom。
func addDep(who, whom *txn) {
	if who.id == whom.id {
		return
	}
	who.deps[whom.id] = struct{}{}
	whom.dependents[who.id] = struct{}{}
}

// validate 先校验事务号、再校验状态；键越界由调用方随后校验。
func (e *Engine) validate(t int, states ...int) (*txn, error) {
	tr, ok := e.txns[t]
	if !ok {
		return nil, ErrUnknownTxn
	}
	for _, s := range states {
		if tr.state == s {
			return tr, nil
		}
	}
	return nil, ErrInvalidState
}

// Read 自新到旧扫描键 key，返回第一个对事务 t 可见的版本值。
// 若可见版本的创建者是处于预备态的他人，t 对其产生提交依赖。
func (e *Engine) Read(t, key int) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	tr, err := e.validate(t, Active)
	if err != nil {
		return 0, err
	}
	if key < 0 || key >= e.k {
		return 0, ErrKeyOutOfRange
	}
	for i := len(e.keys[key]) - 1; i >= 0; i-- {
		v := e.keys[key][i]
		// 创建者已中止的版本是垃圾，直接跳过。
		if v.creator != 0 && e.txns[v.creator].state == Aborted {
			continue
		}
		if !e.startVisible(v, tr) || !e.endVisible(v, tr) {
			continue
		}
		if v.creator != 0 && v.creator != tr.id && e.txns[v.creator].state == Prepared {
			addDep(tr, e.txns[v.creator])
		}
		return v.value, nil
	}
	return 0, nil // 不可达：初始版本对任何活跃事务可见
}

// newestNonGarbage 返回该键最新的非垃圾版本。
func newestNonGarbage(versions []*version, txns map[int]*txn) *version {
	for i := len(versions) - 1; i >= 0; i-- {
		v := versions[i]
		if v.creator != 0 && txns[v.creator].state == Aborted {
			continue
		}
		return v
	}
	return nil
}

// Write 在键 key 上为事务 t 写入值 x。
// 成功返回 (nil, nil)；写冲突时中止 t 并级联，返回 (被中止事务号升序, nil)。
func (e *Engine) Write(t, key, x int) ([]int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	tr, err := e.validate(t, Active)
	if err != nil {
		return nil, err
	}
	if key < 0 || key >= e.k {
		return nil, ErrKeyOutOfRange
	}
	versions := e.keys[key]
	v := newestNonGarbage(versions, e.txns)

	if v.creator == tr.id {
		v.value = x // 自己的最新版本：原地改值
		return nil, nil
	}

	// V 须无有效结束者（无，或结束者已中止）；
	// 创建者（他人）须非活跃，且其 ET < RT。
	enderOK := v.ender == 0
	if !enderOK && e.txns[v.ender].state == Aborted {
		enderOK = true
	}
	cstate, cet := e.creatorState(v.creator)
	creatorOK := cstate != Active && cet < tr.rt
	if !enderOK || !creatorOK {
		return e.abortLocked(tr), nil
	}

	e.keys[key] = append(versions, &version{value: x, creator: tr.id, ender: 0})
	v.ender = tr.id
	if cstate == Prepared {
		addDep(tr, e.txns[v.creator])
	}
	return nil, nil
}

// Precommit 令时钟加一，t 转预备态、ET=n，返回 ET。
func (e *Engine) Precommit(t int) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	tr, err := e.validate(t, Active)
	if err != nil {
		return 0, err
	}
	e.clock++
	tr.state = Prepared
	tr.et = e.clock
	return tr.et, nil
}

// commitLocked 提交一个依赖已空的预备事务，并反复取“预备、已 Finish、
// 依赖已空”的事务中号最小者提交，返回提交次序。
func (e *Engine) commitLocked(first *txn) []int {
	order := make([]int, 0)
	ready := map[int]*txn{first.id: first}
	for len(ready) > 0 {
		ids := make([]int, 0, len(ready))
		for id := range ready {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		id := ids[0]
		cur := ready[id]
		delete(ready, id)
		if cur.state != Prepared || !cur.finished || len(cur.deps) > 0 {
			continue
		}
		cur.state = Committed
		order = append(order, cur.id)

		// 依赖者按号升序解除对 cur 的依赖。
		deps := make([]int, 0, len(cur.dependents))
		for d := range cur.dependents {
			deps = append(deps, d)
		}
		sort.Ints(deps)
		for _, d := range deps {
			dep := e.txns[d]
			delete(dep.deps, cur.id)
			delete(cur.dependents, d)
			if dep.state == Prepared && dep.finished && len(dep.deps) == 0 {
				ready[d] = dep
			}
			// 活跃态依赖者仅移除依赖，不并入提交集合。
		}
		cur.deps = map[int]struct{}{}
		cur.dependents = map[int]struct{}{}
	}
	return order
}

// Finish 将预备且未 Finish 的 t 标记为已 Finish。
// 依赖为空则立即提交并推进提交链，返回本次提交次序；否则等待，返回 nil。
func (e *Engine) Finish(t int) ([]int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	tr, err := e.validate(t, Prepared)
	if err != nil {
		return nil, err
	}
	if tr.finished {
		return nil, ErrInvalidState
	}
	tr.finished = true
	if len(tr.deps) == 0 {
		return e.commitLocked(tr), nil
	}
	return nil, nil
}

// abortLocked 中止 start（须为活跃或预备），并级联中止其全部直接与间接
// 依赖者（含活跃态）；返回事务号升序列表。终结后清空相关依赖集合。
func (e *Engine) abortLocked(start *txn) []int {
	victims := map[int]*txn{start.id: start}
	frontier := []*txn{start}
	for len(frontier) > 0 {
		cur := frontier[len(frontier)-1]
		frontier = frontier[:len(frontier)-1]
		for d := range cur.dependents {
			dep := e.txns[d]
			if dep.state == Committed {
				continue // 不可能依赖一个未终结事务
			}
			if _, seen := victims[d]; !seen {
				victims[d] = dep
				frontier = append(frontier, dep)
			}
		}
	}

	ids := make([]int, 0, len(victims))
	for id := range victims {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	for _, id := range ids {
		tr := victims[id]
		// 拆除所有入边：我依赖的事务不再记我为依赖者。
		for d := range tr.deps {
			delete(e.txns[d].dependents, tr.id)
		}
		tr.deps = map[int]struct{}{}
		tr.dependents = map[int]struct{}{}
		tr.state = Aborted
	}
	return ids
}

// Abort 中止活跃或预备态的 t，并级联中止全部直接与间接依赖者。
func (e *Engine) Abort(t int) ([]int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	tr, err := e.validate(t, Active, Prepared)
	if err != nil {
		return nil, err
	}
	return e.abortLocked(tr), nil
}
