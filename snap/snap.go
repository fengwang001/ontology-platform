// Package snap 只保留最新一份周期快照。
package snap

// Snapshot 是一份快照记录。
type Snapshot struct {
	Tick int64  // 快照帧号 s（P 的倍数）
	Size uint64 // 到该帧为止最近 P 帧的 size 之和
}

// Store 只保留最新快照。零值可用。
type Store struct {
	latest Snapshot
	have   bool
}

// Observe 在帧 tick 落库时被调用；size 为最近 P 帧之和。
func (s *Store) Observe(tick int64, size uint64) {
	s.latest = Snapshot{Tick: tick, Size: size}
	s.have = true
}

// Latest 返回当前快照；第二返回值为 false 表示尚无快照。
func (s *Store) Latest() (Snapshot, bool) { return s.latest, s.have }
