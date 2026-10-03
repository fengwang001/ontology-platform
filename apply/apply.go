// Package apply 编排分片迁移事件：校验、就绪判定、落库、挂起、释放、死信。
package apply

import (
	"errors"
	"sync"

	"ontology/fkpend"
	"ontology/idmap"
)

// 表类型。
const (
	KindDept = 1
	KindEmp  = 2
)

const (
	minShard   = 1
	maxShard   = 16
	minSrcID   = 1
	maxSrcID   = 1_000_000_000
	maxNow     = 1_000_000_000_000
	maxQueue   = 10_000
	maxTimeout = 1_000_000_000
)

var (
	// ErrParam 表示事件或构造参数非法。
	ErrParam = errors.New("bad parameter")
	// ErrDenied 表示分片不在 allow 集合。
	ErrDenied = errors.New("shard not allowed")
	// ErrClock 表示 now 小于已接受事件的最大 now。
	ErrClock = errors.New("clock moved backwards")
	// ErrUnknown 表示 Delete 的键既无挂起行也无存活目标行。
	ErrUnknown = errors.New("unknown key")
	// ErrFull 表示新增挂起行时队列容量不足。
	ErrFull = errors.New("pending queue full")
)

// Status 是事件处理结果状态（骨架）。
type Status int

const (
	// StatusApplied 落库（含替换引用、释放落库）。
	StatusApplied Status = iota
	// StatusPending 进入挂起队列。
	StatusPending
	// StatusDeleted 存活目标行被置为不存活。
	StatusDeleted
	// StatusDroppedPending 仅删除了挂起行。
	StatusDroppedPending
	// 以下为拒绝/错误状态。
	StatusErrUnknown
	StatusErrDenied
	StatusErrFull
	StatusErrParam
	StatusErrClock
)

// Result 是一次事件处理的结果（骨架）。
type Result struct {
	Status Status
	Tid    int64
	Dead   int
}

// Row 是一条存活目标行（快照用）。
type Row struct {
	Key idmap.Key
	Tid int64
	A   int64
	B   int64
}

// Engine 是迁移引擎。所有方法可并发调用，语义等价于某一串行顺序。
type Engine struct {
	mu     sync.Mutex
	t      int64
	q      int
	allow  map[int]bool
	maxNow int64
	idm    *idmap.Store
	pend   *fkpend.Queue
	rows   map[idmap.Key]Row // 存活目标行（A、B 已改写为目标 tid）
}

// New 创建引擎（骨架）。
//
// T 为挂起超时（1..1e9），Q 为挂起队列容量（1..1e4），allow 为可写分片集合。
func New(T int64, Q int, allow []int) (*Engine, error) {
	if T < 1 || T > maxTimeout || Q < 1 || Q > maxQueue || len(allow) == 0 {
		return nil, ErrParam
	}
	set := make(map[int]bool, len(allow))
	for _, s := range allow {
		if s < minShard || s > maxShard {
			return nil, ErrParam
		}
		if set[s] {
			return nil, ErrParam
		}
		set[s] = true
	}
	e := &Engine{
		t:     T,
		q:     Q,
		allow: set,
		idm:   idmap.New(),
		pend:  fkpend.New(Q, T),
		rows:  make(map[idmap.Key]Row),
	}
	return e, nil
}

func validEvent(s, kind int, id int64, now int64) bool {
	if s < minShard || s > maxShard || now < 0 || now > maxNow {
		return false
	}
	if kind != KindDept && kind != KindEmp || id < minSrcID || id > maxSrcID {
		return false
	}
	return true
}

func validRefs(kind int, a, b int64) bool {
	if a < 0 || a > maxSrcID || b < 0 || b > maxSrcID {
		return false
	}
	switch kind {
	case KindDept:
		return b == 0
	case KindEmp:
		return a != 0
	}
	return false
}

// refKey 返回行 src 对某非零源引用 ref 的引用键；dept.a 与 emp.a 指向 dept，
// emp.b 指向 emp；引用恒在同一分片、同一源编号空间。
func refKey(src idmap.Key, isB bool, ref int64) idmap.Key {
	k := idmap.Key{S: src.S, Id: ref}
	if isB {
		k.Kind = KindEmp
	} else {
		k.Kind = KindDept
	}
	return k
}

// waitKey 按 a、b 顺序返回第一个不就绪引用的键；零值表示全部就绪。
// 引用自己的源 id 恒视为就绪（落库时先分配自己的 tid 再改写）。
func (e *Engine) waitKey(k idmap.Key, a, b int64) idmap.Key {
	check := func(rk idmap.Key, ref int64) idmap.Key {
		if ref == 0 || rk == k {
			return idmap.Key{}
		}
		if r, ok := e.idm.Lookup(rk); ok && r.Alive {
			return idmap.Key{}
		}
		return rk
	}
	if wk := check(refKey(k, false, a), a); wk != (idmap.Key{}) {
		return wk
	}
	if k.Kind == KindEmp {
		if wk := check(refKey(k, true, b), b); wk != (idmap.Key{}) {
			return wk
		}
	}
	return idmap.Key{}
}

// rewrite 把非零源引用改写为目标 tid（调用方须保证引用已就绪）。
func (e *Engine) rewrite(k idmap.Key, a, b int64, selfTid int64) (int64, int64) {
	mapRef := func(isB bool, ref int64) int64 {
		if ref == 0 {
			return 0
		}
		rk := refKey(k, isB, ref)
		if rk == k {
			return selfTid
		}
		r, _ := e.idm.Lookup(rk)
		return r.Tid
	}
	return mapRef(false, a), mapRef(true, b)
}

// land 落库一行：分配/沿用 tid、改写引用、写存活表。返回 tid。
func (e *Engine) land(k idmap.Key, a, b int64) int64 {
	tid := e.idm.Alloc(k)
	ta, tb := e.rewrite(k, a, b, tid)
	e.idm.SetAlive(k, true)
	e.rows[k] = Row{Key: k, Tid: tid, A: ta, B: tb}
	return tid
}

// Upsert 处理上行/替换事件。
func (e *Engine) Upsert(s, kind int, id, a, b, now int64) Result {
	k := idmap.Key{S: s, Kind: kind, Id: id}

	// 拒绝次序：参数 → 权限 → 时钟；全部先于到期处理，拒绝不改任何状态。
	if !validEvent(s, kind, id, now) || !validRefs(kind, a, b) {
		return Result{Status: StatusErrParam}
	}
	if !e.allow[s] {
		return Result{Status: StatusErrDenied}
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if now < e.maxNow {
		return Result{Status: StatusErrClock}
	}

	existing := e.pend.Pending(k)
	wk := e.waitKey(k, a, b)
	if wk != (idmap.Key{}) {
		// 到期只把挂起项移入死信，不改变任何引用键的映射/存活，
		// 故就绪性在到期前后一致，可在此先判定容量。
		if !existing && e.pend.Len()-e.pend.ExpiringCount(now)+1 > e.q {
			// 被拒时到期处理也不生效：直接返回，不触碰任何状态（含 maxNow）。
			return Result{Status: StatusErrFull}
		}
	}

	dead := e.pend.Expire(now)
	e.maxNow = now

	existing = e.pend.Pending(k)
	wk = e.waitKey(k, a, b)
	// 就绪：若该键有挂起行（含曾经落库后又挂起的情形），先摘除。
	if wk == (idmap.Key{}) && existing {
		e.pend.RemovePending(k)
	}
	if wk != (idmap.Key{}) {
		en := fkpend.Entry{Key: k, A: a, B: b}
		if existing {
			// 覆盖内容，保留原 aseq 与到达 now；存活行（若曾落库）不动。
			e.pend.Replace(en, wk)
		} else {
			e.pend.Add(en, wk, now)
		}
		return Result{Status: StatusPending, Dead: dead}
	}

	tid := e.land(k, a, b)
	e.releaseFrom(k)
	return Result{Status: StatusApplied, Tid: tid, Dead: dead}
}

// releaseFrom 在调用方已持锁时执行 BFS 释放，封装落库时的状态变更。
func (e *Engine) releaseFrom(root idmap.Key) {
	e.pend.Release(root, func(en fkpend.Entry, _ int, _ int64) idmap.Key {
		wk := e.waitKey(en.Key, en.A, en.B)
		if wk != (idmap.Key{}) {
			return wk
		}
		e.land(en.Key, en.A, en.B)
		return idmap.Key{}
	})
}

// Delete 处理删除事件。
func (e *Engine) Delete(s, kind int, id, now int64) Result {
	k := idmap.Key{S: s, Kind: kind, Id: id}
	if !validEvent(s, kind, id, now) {
		return Result{Status: StatusErrParam}
	}
	if !e.allow[s] {
		return Result{Status: StatusErrDenied}
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if now < e.maxNow {
		return Result{Status: StatusErrClock}
	}

	dead := e.pend.Expire(now)
	e.maxNow = now

	if e.pend.RemovePending(k) {
		return Result{Status: StatusDroppedPending, Dead: dead}
	}
	r, ok := e.idm.Lookup(k)
	if !ok || !r.Alive {
		return Result{Status: StatusErrUnknown, Dead: dead, Tid: errTid(r, ok)}
	}
	e.idm.SetAlive(k, false)
	delete(e.rows, k)
	return Result{Status: StatusDeleted, Tid: r.Tid, Dead: dead}
}

func errTid(r idmap.Row, ok bool) int64 {
	if ok {
		return r.Tid
	}
	return 0
}

// Tick 推进逻辑时钟并完成到期扫描，返回本次移入死信的行数。
func (e *Engine) Tick(now int64) Result {
	if now < 0 || now > maxNow {
		return Result{Status: StatusErrParam}
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if now < e.maxNow {
		return Result{Status: StatusErrClock}
	}

	dead := e.pend.Expire(now)
	e.maxNow = now
	return Result{Status: StatusApplied, Dead: dead}
}

// Dead 返回按进入次序排列的死信键副本。
func (e *Engine) Dead() []idmap.Key {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pend.Dead()
}

// Snapshot 返回当前全部存活目标行（顺序不定，断言时按键排序）。
func (e *Engine) Snapshot() []Row {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Row, 0, len(e.rows))
	for _, r := range e.rows {
		out = append(out, r)
	}
	return out
}

// Next 返回某表下一个待分配 tid（确定性校验用）。
func (e *Engine) Next(kind int) int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.idm.Next(kind)
}

// PendingLen 返回当前挂起行数。
func (e *Engine) PendingLen() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pend.Len()
}

// PendingAll 返回挂起行的测试视图（aseq、到达 now、当前等待键）。
func (e *Engine) PendingAll() []fkpend.PendingInfo {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pend.PendingAll()
}

// Inspected 返回释放检视计数（非导出计数器的只读视图）。
func (e *Engine) Inspected() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pend.Inspected()
}

// ResetInspected 清零释放检视计数。
func (e *Engine) ResetInspected() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pend.ResetInspected()
}
