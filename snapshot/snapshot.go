// Package snapshot 提供只读快照句柄：记录版本水位，
// 通过 store 的写时复制保留旧值，关闭后按最老水位释放。
package snapshot

import (
	"errors"
	"sync"

	"ontology/store"
)

// ErrClosed 表示快照已关闭（过期）；续传、读取遇到它时必须失败。
var ErrClosed = errors.New("snapshot: 已关闭")

// Snapshot 是某一版本水位上的只读视图。
type Snapshot struct {
	st      *store.Store
	version uint64
	mu      sync.RWMutex
	closed  bool
}

// Open 在 st 当前版本上打开一个快照。
func Open(st *store.Store) *Snapshot {
	return &Snapshot{st: st, version: st.RegisterSnapshot()}
}

// Version 返回快照水位。
func (s *Snapshot) Version() uint64 { return s.version }

// Closed 报告快照是否已关闭。
func (s *Snapshot) Closed() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.closed
}

// Get 读取快照时刻 key 的值；快照关闭后返回 ErrClosed。
func (s *Snapshot) Get(key string) ([]byte, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, false, ErrClosed
	}
	v, ok := s.st.GetAt(key, s.version)
	return v, ok, nil
}

// Keys 返回快照时刻可见的键，按字典序（导出的确定性顺序）。
func (s *Snapshot) Keys() []string { return s.st.KeysAt(s.version) }

// Close 关闭快照并释放为它保留的旧值；幂等。
func (s *Snapshot) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.st.ReleaseSnapshot(s.version)
	return nil
}
