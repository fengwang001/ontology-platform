package snapshot

import "sync/atomic"

// Stats 是自服务创建以来的累计统计。
type Stats struct {
	Applies      uint64
	Rejections   uint64
	Inserts      uint64
	Deletes      uint64
	Updates      uint64
	ChangesTotal uint64
}

// Store 持有当前快照与累计统计，支持与差分并发的只读查询。
type Store struct {
	current atomic.Pointer[Snapshot]
	ver     atomic.Uint64
	mu      struct{}

	applies      atomic.Uint64
	rejections   atomic.Uint64
	inserts      atomic.Uint64
	deletes      atomic.Uint64
	updates      atomic.Uint64
	changesTotal atomic.Uint64

	maxChanges int
	logger     Logger
}

// NewStore 创建 Store。maxChanges 必须 > 0，否则返回 ErrInvalidConfig。
func NewStore(maxChanges int, logger Logger) (*Store, error) {
	return nil, nil
}

// Current 返回当前快照的深拷贝及其版本号。
func (s *Store) Current() (Snapshot, uint64) {
	return nil, 0
}

// Stats 返回累计统计的快照值。
func (s *Store) Stats() Stats {
	return Stats{}
}

// Version 返回当前版本号；初值为 0，每次成功差分后递增。
func (s *Store) Version() uint64 { return s.ver.Load() }

// Apply 校验并差分，整体成功后原子切换当前快照；任何失败不留痕。
func (s *Store) Apply(next Snapshot) (ChangeLog, error) {
	return nil, nil
}

// SelfCheck 校验当前快照合法并核对内部不变量，返回当前版本。
func (s *Store) SelfCheck() (uint64, error) {
	return 0, nil
}
