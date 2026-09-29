package snapshot

import "sync/atomic"

// Stats 是差分服务的累计统计（仅统计成功调用）。
type Stats struct {
	CurrentVersion int64
	Merges         int64
	Inserts        int64
	Deletes        int64
	Updates        int64
}

// Store 保存当前快照，支持差分期间的并发只读访问。
type Store struct {
	state atomic.Pointer[storeState]
}

type storeState struct {
	entries []Entry
	version int64
	stats   Stats
}

// NewStore 创建服务，初始快照必须有序。
func NewStore(initial []Entry, cfg Config) (*Store, error) { return nil, nil }

// Snapshot 返回当前快照的不可变副本。
func (s *Store) Snapshot() []Entry { return nil }

// Stats 返回累计统计的副本。
func (s *Store) Stats() Stats { return Stats{} }

// Version 返回当前快照版本号（从 1 开始）。
func (s *Store) Version() int64 { return 0 }

// Merge 校验新快照并与当前快照差分；失败不留痕，成功则原子替换。
func (s *Store) Merge(next []Entry, log Logger) ([]Change, error) { return nil, nil }

// SelfCheck 对当前快照与统计做内部一致性检查。
func (s *Store) SelfCheck() error { return nil }
