// Package snapshot 表示读快照：水位、活跃事务集与自身事务。
package snapshot

import (
	"errors"

	"ontology/txn"
)

// ErrReleased 是可判定错误：快照释放后仍被使用。
var ErrReleased = errors.New("snapshot: snapshot released")

// ErrActiveSetLimit 是可判定错误：活跃集超过构造时给定的上限。
var ErrActiveSetLimit = errors.New("snapshot: active set exceeds limit")

// Snapshot 是不可变的读视图（仅 released 标志会变）。
type Snapshot struct {
	watermark uint64
	owner     txn.ID
	active    map[txn.ID]struct{}
	released  bool
}

// New 构造快照；len(active) 超过 limit 时返回 ErrActiveSetLimit。
// limit <= 0 表示不限制。owner 表示取快照的事务自身，可为任意值。
func New(watermark uint64, active []txn.ID, owner txn.ID, limit int) (*Snapshot, error) {
	if limit > 0 && len(active) > limit {
		return nil, ErrActiveSetLimit
	}
	set := make(map[txn.ID]struct{}, len(active))
	for _, id := range active {
		set[id] = struct{}{}
	}
	return &Snapshot{watermark: watermark, owner: owner, active: set}, nil
}

// Watermark 返回快照水位。
func (s *Snapshot) Watermark() uint64 { return s.watermark }

// Owner 返回取快照的事务号（自己写的自己可见）。
func (s *Snapshot) Owner() txn.ID { return s.owner }

// Len 返回活跃集项数，即快照占用的内存项数。
func (s *Snapshot) Len() int { return len(s.active) }

// Active 判断 t 是否在活跃集中，恰好一次 map 查找。
func (s *Snapshot) Active(t txn.ID) (bool, error) {
	if s.released {
		return false, ErrReleased
	}
	_, ok := s.active[t]
	return ok, nil
}

// Use 检查快照是否仍可使用。
func (s *Snapshot) Use() error {
	if s.released {
		return ErrReleased
	}
	return nil
}

// Release 标记快照已释放，释放后继续判定返回 ErrReleased。
func (s *Snapshot) Release() { s.released = true }
