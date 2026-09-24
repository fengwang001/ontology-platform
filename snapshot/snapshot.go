// Package snapshot 表示读快照：水位、自身事务与活跃事务集。
package snapshot

import (
	"errors"
	"sync"

	"ontology/txn"
)

var (
	// ErrSnapshotReleased 在快照释放后仍被使用时返回。
	ErrSnapshotReleased = errors.New("snapshot: released snapshot must not be used")
	// ErrActiveSetTooLarge 在活跃事务数超过上限时返回。
	ErrActiveSetTooLarge = errors.New("snapshot: active set exceeds limit")
)

// MaxActive 是单个快照活跃集允许的事务数上限。
const MaxActive = 16384

// Snapshot 构造完成后内容不可变；released 受 mu 保护。
type Snapshot struct {
	watermark uint64
	self      txn.ID
	active    map[txn.ID]struct{}

	mu       sync.RWMutex
	released bool
}

// New 构造快照：水位 watermark，self 为取快照的自身事务（0 表示无），
// active 为取快照时刻仍在进行中的事务号集合。
func New(watermark uint64, self txn.ID, active []txn.ID) (*Snapshot, error) {
	if len(active) > MaxActive {
		return nil, ErrActiveSetTooLarge
	}
	set := make(map[txn.ID]struct{}, len(active))
	for _, id := range active {
		set[id] = struct{}{}
	}
	return &Snapshot{watermark: watermark, self: self, active: set}, nil
}

// Watermark 返回快照水位（左闭右开区间的上界）。
func (s *Snapshot) Watermark() uint64 { return s.watermark }

// Self 返回取快照的自身事务号，0 表示无自身事务。
func (s *Snapshot) Self() txn.ID { return s.self }

// ActiveCount 返回活跃集项数，即快照随活跃事务数增长的内存项数。
func (s *Snapshot) ActiveCount() int { return len(s.active) }

// Contains 做一次常数时间查找，判断 id 是否在活跃集中。
func (s *Snapshot) Contains(id txn.ID) bool {
	_, ok := s.active[id]
	return ok
}

// Released 报告快照是否已释放。
func (s *Snapshot) Released() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.released
}

// Release 释放快照；重复释放不报错，之后任何使用都应被拒绝。
func (s *Snapshot) Release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.released = true
}
