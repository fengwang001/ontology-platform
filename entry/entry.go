package entry

import "ontology/version"

// Entry 是单个键的状态机。它本身不加锁；由持有它的 replica 统一加锁保护。
// 状态转移只能通过本文件的方法完成，任何旧版本操作都被原样拒绝。
type Entry struct {
	state   State
	data    []byte
	exists  bool
	dataVer version.Version // 已提交数据的版本（含负缓存）
	seenVer version.Version // 已知最高失效水位
	expire  int64           // 存活到期时刻（注入时钟刻度）；左闭右开

	fetches  uint64 // 本条目实际发起的回源次数
	invalid  uint64 // 本条目实际触发的失效次数
	staleAt  uint64 // Fetching 期间被高版本通知作废的次数（0/1）
	loaded   bool   // 是否曾经成功回源过（区分 Hole 与负缓存）
	bornTick int64  // 创建时刻，供淘汰策略排序
}

// New 创建一个处于 Hole 的新条目。
func New(bornTick int64) *Entry { return &Entry{state: Hole, bornTick: bornTick} }

func (e *Entry) State() State             { return e.state }
func (e *Entry) DataVer() version.Version { return e.dataVer }
func (e *Entry) SeenVer() version.Version { return e.seenVer }
func (e *Entry) Fetches() uint64          { return e.fetches }
func (e *Entry) Invalidations() uint64    { return e.invalid }
func (e *Entry) BornTick() int64          { return e.bornTick }

// Expired 按左闭右开判定：now == expire 即算过期。
func (e *Entry) Expired(now int64) bool {
	return e.state == Valid && now >= e.expire
}

// LazyExpire 执行惰性过期：Valid 到期后转为 Stale（数据保留作为回源前快照）。
func (e *Entry) LazyExpire(now int64) bool {
	if !e.Expired(now) {
		return false
	}
	e.state = Stale
	return true
}

// NeedFetch 报告当前是否需要（重新）回源。
func (e *Entry) NeedFetch(now int64) bool {
	switch e.state {
	case Hole, Stale:
		return true
	case Valid:
		return now >= e.expire
	default:
		return false
	}
}

// BeginFetch 进入 Fetching。只有 Hole/Stale/已过期 Valid 可发起。
// 返回 false 表示当前不允许回源（已在回源中或数据仍新鲜）。
func (e *Entry) BeginFetch(now int64) bool {
	switch {
	case e.state == Fetching:
		return false
	case e.state == Valid && now < e.expire:
		return false
	}
	if e.state == Valid {
		e.state = Stale
	}
	e.state = Fetching
	e.staleAt = 0
	e.fetches++
	return true
}
