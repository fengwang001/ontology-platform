// Package snapshot 提供记录版本水位的只读快照句柄。
package snapshot

import (
	"errors"
	"sync"

	"ontology/store"
)

// ErrClosed 表示快照已关闭，拒绝新的读取授权或续传。
var ErrClosed = errors.New("snapshot: 快照已关闭")

// Snapshot 记录版本水位与创建时刻的键集合；旧值由 store 的写时复制保留。
type Snapshot struct {
	st      *store.Store
	ver     uint64
	keys    []string
	release func()

	mu       sync.Mutex
	cond     *sync.Cond
	closed   bool
	inflight int
	released bool
}

// Open 在当前版本上打开一个快照。
func Open(st *store.Store) *Snapshot {
	ver := st.Version()
	s := &Snapshot{st: st, ver: ver, release: st.Pin(ver), keys: st.KeysAt(ver)}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// Version 返回快照的版本水位。
func (s *Snapshot) Version() uint64 { return s.ver }

// Keys 返回快照时刻的键集合（字典序）。
func (s *Snapshot) Keys() []string { return append([]string(nil), s.keys...) }

// Acquire 授权一次导出读取；快照已关闭则返回 ErrClosed。
func (s *Snapshot) Acquire() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	s.inflight++
	return nil
}

// Release 归还一次 Acquire 的授权。
func (s *Snapshot) Release() {
	s.mu.Lock()
	s.inflight--
	if s.inflight == 0 {
		s.cond.Broadcast()
	}
	s.mu.Unlock()
}

// Get 读取快照时刻键的值；调用前须 Acquire。
func (s *Snapshot) Get(key string) ([]byte, bool) {
	return s.st.GetAt(key, s.ver)
}

// Close 关闭快照：拒绝新授权，等待在飞读取结束后释放全部保留值。
func (s *Snapshot) Close() {
	s.mu.Lock()
	s.closed = true
	for s.inflight > 0 {
		s.cond.Wait()
	}
	s.mu.Unlock()
	s.mu.Lock()
	if !s.released {
		s.released = true
		s.release()
	}
	s.mu.Unlock()
}

// Closed 报告快照是否已关闭。
func (s *Snapshot) Closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}
