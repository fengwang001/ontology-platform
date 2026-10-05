// Package snap 维护周期快照，只保留最新一份。
package snap

// Store 保存最新快照；同一时刻只保留一份。
type Store struct {
	period   int
	tick     int
	snapSize int64
}

// New 创建周期为 p 的快照存储。
func New(p int) *Store { return &Store{period: p} }

// Observe 在帧 tick 追加后调用；tick 为周期倍数时以给定大小刷新快照。
func (s *Store) Observe(tick int, size int64) {
	if tick%s.period != 0 {
		return
	}
	s.tick = tick
	s.snapSize = size
}

// Tick 返回最新快照帧号；无快照为 0。
func (s *Store) Tick() int { return s.tick }

// Size 返回最新快照大小。
func (s *Store) Size() int64 { return s.snapSize }
