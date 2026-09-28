package idempotent

import (
	"context"
	"sync"
)

type memStore struct {
	mu   sync.Mutex
	snap *snapshot
}

func newMemStore() *memStore {
	return &memStore{}
}

func (m *memStore) Load(_ context.Context) (*snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.snap == nil {
		return newSnapshot(), nil
	}
	return m.snap.clone(), nil
}

func (m *memStore) Commit(_ context.Context, snap *snapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snap = snap.clone()
	return nil
}

// flakyStore 在成功前让前 failNext 次 Commit 失败，模拟崩溃/提交失败。
type flakyStore struct {
	inner    *memStore
	mu       sync.Mutex
	failNext int
}

func (f *flakyStore) Load(ctx context.Context) (*snapshot, error) {
	return f.inner.Load(ctx)
}

func (f *flakyStore) Commit(ctx context.Context, snap *snapshot) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext > 0 {
		f.failNext--
		return context.DeadlineExceeded
	}
	return f.inner.Commit(ctx, snap)
}

func newSnapshot() *snapshot {
	return &snapshot{
		Version:    1,
		Watermarks: map[int]int64{},
		Duplicates: map[int]int64{},
		Results:    map[string]int64{},
	}
}

func (s *snapshot) clone() *snapshot {
	return &snapshot{
		Version:    s.Version,
		Watermarks: cloneInt64Map(s.Watermarks),
		Duplicates: cloneInt64Map(s.Duplicates),
		Results:    cloneStringMap(s.Results),
	}
}
