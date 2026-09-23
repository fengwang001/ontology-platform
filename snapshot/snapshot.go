// Package snapshot 实现读快照：建立时固化快照点与活跃事务集合，
// 之后对任意提交号的可见性判定是纯函数（可重复读）。
package snapshot

import "ontology/txid"

// Snapshot 是只读事务的可见性边界。左闭右开：
// 提交号 < Point 且不在活跃集合中的版本可见；等于 Point 的不可见。
type Snapshot struct {
	Point     txid.T
	active    map[txid.T]struct{}
	minActive txid.T
}

// New 建立快照。point 取事务号源的 Peek()；active 是建立时刻
// 所有未提交写事务的号（均 < point）。
func New(point txid.T, active []txid.T) Snapshot {
	s := Snapshot{
		Point:     point,
		active:    make(map[txid.T]struct{}, len(active)),
		minActive: point,
	}
	for _, a := range active {
		s.active[a] = struct{}{}
		if a < s.minActive {
			s.minActive = a
		}
	}
	return s
}

// Visible 判定提交号 commit 对本快照是否可见。
func (s Snapshot) Visible(commit txid.T) bool {
	if commit == txid.Invalid || commit >= s.Point {
		return false
	}
	_, blocked := s.active[commit]
	return !blocked
}

// IsActive 判定事务号在快照建立时是否活跃（供 version 包判定用）。
func (s Snapshot) IsActive(t txid.T) bool {
	_, ok := s.active[t]
	return ok
}

// floor 返回 min(Point, min(active))：本快照仍可能看到的最早提交号
// 的下界。任何提交号 < floor 的版本对本快照一定可见。
func (s Snapshot) floor() txid.T {
	return s.minActive
}

// Registry 跟踪全部活跃快照，供回收器计算水位。
type Registry struct {
	snaps map[uint64]Snapshot
	next  uint64
}

// NewRegistry 返回空注册表。
func NewRegistry() *Registry {
	return &Registry{snaps: make(map[uint64]Snapshot)}
}

// Open 注册一个快照，返回句柄 id。
func (r *Registry) Open(s Snapshot) uint64 {
	r.next++
	r.snaps[r.next] = s
	return r.next
}

// Close 注销快照。
func (r *Registry) Close(id uint64) {
	delete(r.snaps, id)
}

// Len 返回活跃快照数。
func (r *Registry) Len() int {
	return len(r.snaps)
}

// Horizon 返回回收水位候选：所有活跃快照 floor 的最小值；
// 无活跃快照时返回 now（一切被遮蔽版本都可回收）。
func (r *Registry) Horizon(now txid.T) txid.T {
	h := now
	for _, s := range r.snaps {
		if f := s.floor(); f < h {
			h = f
		}
	}
	return h
}
