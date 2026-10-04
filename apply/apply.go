// Package apply 把多个分片各自编号的源行合并写入同一目标库：
// 负责事件校验、时钟与权限检查、主键重映射（经 idmap）、外键改写、
// 未就绪行的挂起与释放（经 fkpend）、删除与死信。
//
// 所有导出方法都持有同一把互斥锁，并发调用等价于某个串行顺序。
package apply

import (
	"errors"
	"sync"

	"ontology/fkpend"
	"ontology/idmap"
)

// 表 kind。
const (
	KindDept = 1
	KindEmp  = 2
)

// 取值范围。
const (
	maxShard = 16
	maxID    = int64(1_000_000_000)
	maxNow   = int64(1_000_000_000_000)
	maxT     = int64(1_000_000_000)
	maxQ     = 10_000
	numKinds = 2
)

// 事件结果与拒绝原因。
var (
	ErrInvalid = errors.New("apply: invalid argument")
	ErrDenied  = errors.New("apply: shard not allowed")
	ErrClock   = errors.New("apply: clock regression")
	ErrUnknown = errors.New("apply: unknown key")
	ErrFull    = errors.New("apply: pending queue full")
)

// Outcome 是事件被接受后的结果类别。
type Outcome int

const (
	Applied Outcome = iota + 1
	Pending
	DroppedPending
	Deleted
)

func (o Outcome) String() string {
	switch o {
	case Applied:
		return "Applied"
	case Pending:
		return "Pending"
	case DroppedPending:
		return "DroppedPending"
	case Deleted:
		return "Deleted"
	}
	return "?"
}

// Result 是 Upsert/Delete 的返回：类别 + 落库时的目标 tid。
type Result struct {
	Outcome Outcome
	Tid     int64 // 仅 Applied 有效
}

// Key 是源侧键（分片、表、源 id），与 fkpend.Key 相同。
type Key = fkpend.Key

// targetRow 是目标表中的一行：tid、改写后的引用 tid 与存活标志。
type targetRow struct {
	tid   int64
	a, b  int64
	alive bool
}

// Merger 是合并迁移器。
type Merger struct {
	mu     sync.Mutex
	allow  map[int]bool
	maps   [numKinds + 1]*idmap.Mapper
	rows   [numKinds + 1]map[Key]*targetRow
	pend   *fkpend.Queue
	dead   []Key
	maxNow int64
}

// New 构造合并迁移器：t 为挂起超时（1..1e9），q 为挂起队列容量（1..1e4），
// allow 为可写分片集合（每个元素须在 1..16）。
func New(t int64, q int, allow []int) (*Merger, error) {
	if t < 1 || t > maxT || q < 1 || q > maxQ {
		return nil, ErrInvalid
	}
	allowSet := make(map[int]bool, len(allow))
	for _, s := range allow {
		if s < 1 || s > maxShard {
			return nil, ErrInvalid
		}
		allowSet[s] = true
	}
	m := &Merger{allow: allowSet, pend: fkpend.New(q, t)}
	for k := 1; k <= numKinds; k++ {
		m.maps[k] = idmap.New()
		m.rows[k] = make(map[Key]*targetRow)
	}
	return m, nil
}

// Upsert 处理事件 Upsert(s, kind, id, a, b, now)。
func (m *Merger) Upsert(s, kind int, id, a, b, now int64) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !validShard(s) || !validKind(kind) || !validID(id) || !validRef(a) || !validRef(b) || !validNow(now) {
		return Result{}, ErrInvalid
	}
	if kind == KindDept && b != 0 {
		return Result{}, ErrInvalid
	}
	if kind == KindEmp && a == 0 {
		return Result{}, ErrInvalid
	}
	if !m.allow[s] {
		return Result{}, ErrDenied
	}
	if now < m.maxNow {
		return Result{}, ErrClock
	}
	m.expire(now)

	key := Key{Shard: s, Kind: kind, ID: id}
	wait, ready := m.waitKey(s, kind, id, a, b)
	if ready {
		m.maxNow = now
		m.pend.Remove(key) // 该键若有挂起行则丢弃，以较新事件为准
		tid := m.land(s, kind, id, a, b)
		m.release(key)
		return Result{Outcome: Applied, Tid: tid}, nil
	}
	if m.pend.Has(key) {
		m.maxNow = now
		m.pend.Replace(key, a, b, wait)
		return Result{Outcome: Pending}, nil
	}
	if m.pend.Full() {
		// 到期处理之后队列仍满；此时到期处理必然没有移出任何行，
		// 故返回 ErrFull 不改变映射、计数器、挂起队列、死信与最大 now。
		return Result{}, ErrFull
	}
	m.maxNow = now
	m.pend.Add(key, a, b, wait, now)
	return Result{Outcome: Pending}, nil
}

// Delete 处理事件 Delete(s, kind, id, now)。
func (m *Merger) Delete(s, kind int, id, now int64) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !validShard(s) || !validKind(kind) || !validID(id) || !validNow(now) {
		return Result{}, ErrInvalid
	}
	if !m.allow[s] {
		return Result{}, ErrDenied
	}
	if now < m.maxNow {
		return Result{}, ErrClock
	}
	key := Key{Shard: s, Kind: kind, ID: id}
	if m.pend.Has(key) {
		m.maxNow = now
		m.expire(now)
		m.pend.Remove(key)
		return Result{Outcome: DroppedPending}, nil
	}
	if tr := m.rows[kind][key]; tr != nil && tr.alive {
		m.maxNow = now
		m.expire(now)
		tr.alive = false
		return Result{Outcome: Deleted}, nil
	}
	return Result{}, ErrUnknown
}

// Tick 处理事件 Tick(now)，返回本次移入死信的行数。
func (m *Merger) Tick(now int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !validNow(now) {
		return 0, ErrInvalid
	}
	if now < m.maxNow {
		return 0, ErrClock
	}
	m.maxNow = now
	return m.expire(now), nil
}

// Dead 按进入次序返回死信键。
func (m *Merger) Dead() []Key {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Key, len(m.dead))
	copy(out, m.dead)
	return out
}

// Target 返回键对应的目标行（tid、改写后的引用 tid、存活标志）。
func (m *Merger) Target(s, kind int, id int64) (tid, a, b int64, alive, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tr := m.rows[kind][Key{Shard: s, Kind: kind, ID: id}]
	if tr == nil {
		return 0, 0, 0, false, false
	}
	return tr.tid, tr.a, tr.b, tr.alive, true
}

// PendingRow 返回键的挂起行；ok 为 false 表示无挂起行。
func (m *Merger) PendingRow(s, kind int, id int64) (fkpend.Row, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pend.Row(Key{Shard: s, Kind: kind, ID: id})
}

// PendingLen 返回当前挂起行数。
func (m *Merger) PendingLen() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pend.Len()
}

// expire 把到期挂起行移入死信，返回移入数量。
func (m *Merger) expire(now int64) int {
	rows := m.pend.Expire(now)
	for _, r := range rows {
		m.dead = append(m.dead, r.Key)
	}
	return len(rows)
}

// waitKey 判定行是否就绪：按 a、b 顺序检查每个非零引用，
// 返回第一个不就绪的引用键；全部就绪时 ready 为 true。
func (m *Merger) waitKey(s, kind int, id, a, b int64) (Key, bool) {
	for _, ref := range refsOf(kind, a, b) {
		rk, rid := ref.kind, ref.id
		if rk == kind && rid == id {
			continue // 引用自己的源 id 视为就绪
		}
		rkey := Key{Shard: s, Kind: rk, ID: rid}
		if _, ok := m.maps[rk].Tid(idmap.Key{Shard: s, ID: rid}); !ok {
			return rkey, false
		}
		if tr := m.rows[rk][rkey]; tr == nil || !tr.alive {
			return rkey, false
		}
	}
	return Key{}, true
}

// land 落库一行：分配（或沿用）tid，改写引用，置为存活，返回 tid。
func (m *Merger) land(s, kind int, id, a, b int64) int64 {
	key := Key{Shard: s, Kind: kind, ID: id}
	tid := m.maps[kind].Assign(idmap.Key{Shard: s, ID: id})
	tr := &targetRow{tid: tid, alive: true}
	for _, ref := range refsOf(kind, a, b) {
		var rtid int64
		if ref.kind == kind && ref.id == id {
			rtid = tid // 自引用改写为自己的 tid
		} else {
			rtid, _ = m.maps[ref.kind].Tid(idmap.Key{Shard: s, ID: ref.id})
		}
		if ref.kind == KindDept {
			tr.a = rtid
		} else {
			tr.b = rtid
		}
	}
	m.rows[kind][key] = tr
	return tid
}

// release 从刚落库的键开始广度优先释放挂起行。
func (m *Merger) release(trigger Key) {
	m.pend.Release(trigger, func(r *fkpend.Row) (bool, Key) {
		wait, ready := m.waitKey(r.Key.Shard, r.Key.Kind, r.Key.ID, r.A, r.B)
		if !ready {
			return false, wait
		}
		m.land(r.Key.Shard, r.Key.Kind, r.Key.ID, r.A, r.B)
		return true, Key{}
	})
}

type refSpec struct {
	kind int
	id   int64
}

// refsOf 按 a、b 顺序返回行的非零引用（dept 只有 a，emp 有 a 与可选的 b）。
func refsOf(kind int, a, b int64) []refSpec {
	if kind == KindDept {
		if a == 0 {
			return nil
		}
		return []refSpec{{KindDept, a}}
	}
	refs := []refSpec{{KindDept, a}}
	if b != 0 {
		refs = append(refs, refSpec{KindEmp, b})
	}
	return refs
}

func validShard(s int) bool   { return s >= 1 && s <= maxShard }
func validKind(k int) bool    { return k == KindDept || k == KindEmp }
func validID(id int64) bool   { return id >= 1 && id <= maxID }
func validRef(r int64) bool   { return r >= 0 && r <= maxID }
func validNow(now int64) bool { return now >= 0 && now <= maxNow }
