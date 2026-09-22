package replica

import "ontology/entry"

// admitLocked 为新键接纳一个空洞条目。调用方必须持有 r.mu。
//
// 条目数超限时的淘汰策略：淘汰"最近最少使用且当前不在回源中"
// 的条目（LRU 序号最小者）；若所有条目都在回源中（被单飞钉住），
// 返回 ErrTooManyEntries，此时不创建条目、不淘汰、不改任何状态。
// 被淘汰条目的后续旧通知按未知键处理（应用时直接丢弃），
// 因此淘汰不会破坏任何存活条目的版本单调性。
func (r *Replica) admitLocked(key string) (*entry.Entry, error) {
	if r.maxCap > 0 && len(r.entries) >= r.maxCap {
		victim := r.evictableLocked()
		if victim == "" {
			return nil, ErrTooManyEntries
		}
		delete(r.entries, victim)
	}
	e := entry.New(key)
	e.Touch(r.nextSeqLocked())
	r.entries[key] = e
	return e, nil
}

// evictableLocked 返回可淘汰条目中 LRU 序号最小的键，无可淘汰者返回空串。
func (r *Replica) evictableLocked() string {
	best := ""
	var bestSeq uint64
	for k, e := range r.entries {
		if r.group.InFlight(k) {
			continue // 回源中的条目被钉住，不可淘汰
		}
		if best == "" || e.LastAccess() < bestSeq {
			best = k
			bestSeq = e.LastAccess()
		}
	}
	return best
}
