// Package snapshot 提供读快照：活跃事务集合与左闭右开的可见性判定。
package snapshot

import (
	"sync"

	"ontology/txid"
)

// Snapshot 是某一时刻数据库状态的冻结视图。
// Point 为"下一个将分配的号"；可见提交号区间为 [1, Point)，
// 且提交事务不在 Active 中。
type Snapshot struct {
	Point  txid.TXID
	active map[txid.TXID]struct{}
	closed bool
}

// New 建立快照：point 为当前即将发出的号，active 为建立瞬间未决事务的拷贝。
func New(point txid.TXID, active map[txid.TXID]struct{}) *Snapshot {
	cp := make(map[txid.TXID]struct{}, len(active))
	for t := range active {
		cp[t] = struct{}{}
	}
	return &Snapshot{Point: point, active: cp}
}

// Visible 实现 ct < point 且 ct 不在活跃集合（左闭右开）。
func (s *Snapshot) Visible(ct txid.TXID) bool {
	return ct.Valid() && ct.Before(s.Point) && !s.Active(ct)
}

// Active 报告事务号在建立快照时是否仍未决。
func (s *Snapshot) Active(t txid.TXID) bool {
	_, ok := s.active[t]
	return ok
}

// Registry 跟踪当前已登记、未关闭的快照，供回收水位计算使用。
type Registry struct {
	mu   sync.Mutex
	live map[*Snapshot]struct{}
}

// NewRegistry 创建空的快照登记表。
func NewRegistry() *Registry { return &Registry{live: map[*Snapshot]struct{}{}} }

// Add 登记一个新快照（必须在 store 锁内、快照对外可见之前完成）。
func (r *Registry) Add(s *Snapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.live[s] = struct{}{}
}

// Remove 关闭快照：此后它不再抬高安全水位。
func (r *Registry) Remove(s *Snapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.live, s)
	s.closed = true
}

// Count 返回当前活跃快照数。
func (r *Registry) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.live)
}

// Horizon 返回所有活跃快照 point 的最小值；无快照时返回 0。
// 回收只能遮蔽提交号严格小于该值的版本。
func (r *Registry) Horizon() txid.TXID {
	r.mu.Lock()
	defer r.mu.Unlock()
	var h txid.TXID
	for s := range r.live {
		if h == 0 || s.Point.Before(h) {
			h = s.Point
		}
	}
	return h
}

// Closed 报告快照是否已关闭。
func (s *Snapshot) Closed() bool { return s.closed }
