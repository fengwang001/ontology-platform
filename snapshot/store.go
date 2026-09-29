package snapshot

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
)

// Store 持有当前快照与累计统计，支持差分与查询/自检并发执行。
type Store struct {
	current atomic.Pointer[[]Entry]

	diffMu sync.Mutex
	stats  Stats

	cfg Config
}

// NewStore 创建空快照的 Store。
func NewStore(cfg Config) *Store {
	s := &Store{cfg: cfg}
	empty := []Entry{}
	s.current.Store(&empty)
	return s
}

// ApplyDiff 校验新快照并与当前快照归并；被整体拒绝时当前快照与统计不变。
func (s *Store) ApplyDiff(ctx context.Context, newSnap []Entry) ([]Change, error) {
	// 归并在锁外完成：读者通过原子指针始终能读到某一时刻的完整版本，
	// 不会看到新旧混合；其他写者随后串行提交。
	oldSnap := s.Snapshot()
	changes, err := Diff(ctx, s.cfg, oldSnap, newSnap)
	if err != nil {
		return nil, err
	}

	next := Replay(oldSnap, changes)

	s.diffMu.Lock()
	defer s.diffMu.Unlock()

	// 持锁后重新确认：本写者在等待期间没有被其他写者抢先提交。
	// 若已抢先，则基于最新快照重新归并并重放（输入仍为同一份新快照）。
	cur := s.Snapshot()
	if !equalEntries(cur, oldSnap) {
		oldSnap = cur
		if changes, err = Diff(ctx, s.cfg, oldSnap, newSnap); err != nil {
			return nil, err
		}
		next = Replay(oldSnap, changes)
	}

	s.current.Store(&next)

	stats := s.stats
	stats.Applied++
	for _, ch := range changes {
		switch ch.Op {
		case OpInsert:
			stats.Inserted++
		case OpDelete:
			stats.Deleted++
		case OpUpdate:
			stats.Updated++
		}
	}
	s.stats = stats

	return changes, nil
}

// Snapshot 返回当前快照的整份副本（某一时刻的完整版本）。
func (s *Store) Snapshot() []Entry {
	p := s.current.Load()
	if p == nil {
		return []Entry{}
	}
	return slices.Clone(*p)
}

// Stats 返回累计统计的副本。
func (s *Store) Stats() Stats {
	s.diffMu.Lock()
	defer s.diffMu.Unlock()
	return s.stats
}

// Verify 自检：当前快照自身严格有序，且最近一次状态可复现。
func (s *Store) Verify() error {
	return Validate(s.Snapshot())
}

func equalEntries(a, b []Entry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
