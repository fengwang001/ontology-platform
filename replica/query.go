package replica

import (
	"time"

	"ontology/bus"
	"ontology/entry"
	"ontology/version"
)

// Attach 把副本订阅到失效通知总线。副本之间不直接通信。
func (r *Replica) Attach(b *bus.Bus) {
	b.Subscribe(r.applyNotification)
}

// applyNotification 应用一条失效通知。只触及受影响的键：
// 一次哈希定位，checkedEntries 恒加 1，与缓存总条目数无关。
//
// 未知键（未缓存或已淘汰）也会接纳一个空洞条目来记录失效版本：
// 否则"通知先到、回源后到"的竞态会让旧版本数据进入缓存。
// 条目数超限且无可淘汰条目时通知被丢弃（由 TTL 到期兜底）。
func (r *Replica) applyNotification(n bus.Notification) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checkedEntries++
	e := r.entries[n.Key]
	if e == nil {
		var err error
		if e, err = r.admitLocked(n.Key); err != nil {
			return
		}
	}
	if e.ApplyInvalidate(n.Version) {
		r.invalidations++
	}
}

// KeyInfo 是某键当前状态的只读快照。
type KeyInfo struct {
	Exists        bool            // 条目对象是否存在
	State         entry.State     // 考虑惰性过期与在途回源后的状态
	Version       version.Version // 当前持有数据的版本
	Invalidated   version.Version // 已应用的最高失效版本
	Found         bool            // 负缓存时为 false
	TTLRemaining  time.Duration   // 剩余存活时长，非存活为 0
	Hits          uint64
	Misses        uint64
	Refetches     uint64
	Invalidations uint64
}

// Inspect 查询某键的当前状态。不推进除惰性过期（按视图计算）
// 以外的任何状态；同一时刻连查两次结果完全相同；未知键返回零值。
func (r *Replica) Inspect(key string) KeyInfo {
	now := r.clock()
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.entries[key]
	if e == nil {
		return KeyInfo{}
	}
	st := e.EffectiveState(now)
	if r.group.InFlight(key) {
		st = entry.Loading
	}
	es := e.Stats()
	return KeyInfo{
		Exists:        true,
		State:         st,
		Version:       e.Version(),
		Invalidated:   e.InvalidatedVersion(),
		Found:         e.Found(),
		TTLRemaining:  e.TTLRemaining(now),
		Hits:          es.Hits,
		Misses:        es.Misses,
		Refetches:     es.Refetches,
		Invalidations: es.Invalidations,
	}
}

// Stats 是副本级只读计数快照。
type Stats struct {
	Entries        int    // 当前条目数
	Hits           uint64 // 命中次数
	Misses         uint64 // 未命中次数
	Refetches      uint64 // 回源成功写入次数
	Invalidations  uint64 // 实际触发的失效次数（重复/过期通知不计）
	CheckedEntries uint64 // 应用通知时累计检查的条目数
	LoaderCalls    int64  // 回源函数实际被调用次数（单飞合并后）
}

// Stats 返回副本级计数快照，不推进任何状态。
func (r *Replica) Stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Stats{
		Entries:        len(r.entries),
		Hits:           r.hits,
		Misses:         r.misses,
		Refetches:      r.refetches,
		Invalidations:  r.invalidations,
		CheckedEntries: r.checkedEntries,
		LoaderCalls:    r.group.Calls(),
	}
}

// InFlightWaiters 返回指定键当前合并等待回源结果的调用者数。
func (r *Replica) InFlightWaiters(key string) int64 {
	return r.group.Waiters(key)
}
