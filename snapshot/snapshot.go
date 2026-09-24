// Package snapshot 表示读快照：水位 watermark 与创建时的活跃事务集。
package snapshot

import (
	"errors"
	"sync/atomic"
)

// MaxActive 是单个快照活跃集大小的资源上限。
const MaxActive = 4096

var (
	// ErrActiveLimit 表示活跃集大小超过 MaxActive。
	ErrActiveLimit = errors.New("snapshot: active set exceeds limit")
	// ErrReleased 表示快照已释放后仍被使用。
	ErrReleased = errors.New("snapshot: snapshot released")
)

// Snapshot 是不可变的读快照。构造后 watermark/active 只读，可并发使用；
// 释放状态通过原子量管理。
type Snapshot struct {
	watermark int64
	owner     int64 // 取得该快照的读事务自身（可在活跃集中）
	active    map[int64]struct{}
	released  atomic.Bool
}

// New 构造快照。watermark 为水位，owner 为快照自身事务，active 为活跃事务号集合。
func New(watermark, owner int64, active []int64) (*Snapshot, error) {
	if len(active) > MaxActive {
		return nil, ErrActiveLimit
	}
	set := make(map[int64]struct{}, len(active))
	for _, id := range active {
		set[id] = struct{}{}
	}
	return &Snapshot{watermark: watermark, owner: owner, active: set}, nil
}

// Use 在使用快照前检查其是否仍有效，已释放返回 ErrReleased。
func (s *Snapshot) Use() error {
	if s.released.Load() {
		return ErrReleased
	}
	return nil
}

// Release 释放快照；之后任何判定都应得到 ErrReleased。
func (s *Snapshot) Release() { s.released.Store(true) }

// Watermark 返回水位（左闭右开：commitTS == watermark 不可见）。
func (s *Snapshot) Watermark() int64 { return s.watermark }

// Owner 返回取得该快照的自身事务号。
func (s *Snapshot) Owner() int64 { return s.owner }

// Contains 做一次活跃集 membership 查找。
func (s *Snapshot) Contains(id int64) bool {
	_, ok := s.active[id]
	return ok
}

// Items 返回该快照占用的内存项数，恰等于活跃集大小，不随事务总数增长。
func (s *Snapshot) Items() int { return len(s.active) }
