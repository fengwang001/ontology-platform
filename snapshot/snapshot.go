// Package snapshot 提供记录版本水位的只读快照句柄。
package snapshot

import (
	"errors"
	"sync"

	"ontology/store"
)

// ErrClosed 表示快照已关闭，旧值可能已释放，不能再读或续传。
var ErrClosed = errors.New("snapshot: snapshot closed")

// Snapshot 绑定某一版本；存活期间 store 为其保留被覆盖的旧值。
type Snapshot struct {
	st  *store.Store
	ver int64

	mu      sync.Mutex
	closed  bool
	exports sync.WaitGroup
}

// New 在当前版本上建立快照。
func New(st *store.Store) *Snapshot {
	v := st.Version()
	st.RegisterSnapshot(v)
	return &Snapshot{st: st, ver: v}
}

// Version 返回快照版本水位。
func (s *Snapshot) Version() int64 { return s.ver }

// beginExport 登记一次导出；已关闭则拒绝，防止半截文件。
func (s *Snapshot) beginExport() bool {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false
	}
	s.exports.Add(1)
	s.mu.Unlock()
	return true
}

func (s *Snapshot) endExport() { s.exports.Done() }

// read 读取快照版本可见的键；关闭后返回 ErrClosed。
func (s *Snapshot) Read(key string) ([]byte, bool, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, false, ErrClosed
	}
	s.mu.Unlock()
	val, ok := s.st.Read(s.ver, key)
	return val, ok, nil
}

func (s *Snapshot) Keys(order string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	return s.st.Keys(s.ver, order), nil
}

// Close 等待所有进行中的导出结束，再释放为该快照保留的旧值。
func (s *Snapshot) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	s.exports.Wait()
	s.st.UnregisterSnapshot(s.ver)
}
