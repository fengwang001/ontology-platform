// Package snapshot 提供固定版本水位的只读快照，写时复制保留被覆盖的旧值。
package snapshot

import (
	"errors"
	"sync"

	"ontology/store"
)

// ErrClosed 表示快照已关闭，不能再用于读取或续传。
var ErrClosed = errors.New("snapshot: snapshot closed")

// Snapshot 记录某一版本的键集合，并保存导出期间被改写的旧值。
type Snapshot struct {
	st     *store.Store
	mu     sync.Mutex
	ver    uint64
	keys   []string
	old    map[string][]byte
	closed bool
	reads  int
}

// Begin 在当前版本创建快照。
func Begin(st *store.Store) *Snapshot {
	s := &Snapshot{st: st, old: make(map[string][]byte)}
	s.ver, s.keys = st.Register(s)
	return s
}

// SnapVersion 实现 store.Retainer。
func (s *Snapshot) SnapVersion() uint64 { return s.ver }

// Retain 实现 store.Retainer：仅在 store 写锁内被调用，每键保留一次。
func (s *Snapshot) Retain(key string, oldValue []byte) {
	if _, ok := s.old[key]; ok {
		return
	}
	s.old[key] = append([]byte(nil), oldValue...)
	s.st.AddRetained(1)
}

// Release 实现 store.Retainer（释放统一在 Close 中完成）。
func (s *Snapshot) Release(dec func()) { dec() }

// Version 返回快照版本水位。
func (s *Snapshot) Version() uint64 { return s.ver }

// Keys 返回快照时刻的键集合副本，顺序按 less 排序；less 为 nil 时字典序。
func (s *Snapshot) Keys(less func(a, b string) bool) []string {
	s.mu.Lock()
	keys := append([]string(nil), s.keys...)
	s.mu.Unlock()
	sortKeys(keys, less)
	return keys
}

// Get 返回快照版本可见的值，优先读保留值；读取计数加一。
func (s *Snapshot) Get(key string) ([]byte, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrClosed
	}
	s.reads++
	if v, ok := s.old[key]; ok {
		s.mu.Unlock()
		return append([]byte(nil), v...), nil
	}
	s.mu.Unlock()

	v, ok := s.st.Current(key)
	if !ok {
		return nil, nil
	}
	return v, nil
}

// Reads 返回累计读取次数。
func (s *Snapshot) Reads() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads
}

// Retained 返回本快照保留的旧值个数。
func (s *Snapshot) Retained() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.old)
}

// Closed 报告快照是否已关闭。
func (s *Snapshot) Closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// ExportBegin 标记一次导出开始；快照已关闭则失败。
func (s *Snapshot) ExportBegin() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	s.st.BeginExport()
	return nil
}

// ExportEnd 标记一次导出结束。
func (s *Snapshot) ExportEnd() { s.st.EndExport() }

// Store 返回底层存储。
func (s *Snapshot) Store() *store.Store { return s.st }

// Close 释放全部保留值，并等待进行中的导出结束。可重复调用。
func (s *Snapshot) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	n := len(s.old)
	s.old = make(map[string][]byte)

	defer s.mu.Unlock()
	s.st.AddRetained(-n)
	s.st.Unregister(s)
	s.st.WaitExports()
}

func sortKeys(keys []string, less func(a, b string) bool) {
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0; j-- {
			if less != nil {
				if less(keys[j], keys[j-1]) {
					keys[j], keys[j-1] = keys[j-1], keys[j]
				} else {
					break
				}
			} else if keys[j] < keys[j-1] {
				keys[j], keys[j-1] = keys[j-1], keys[j]
			} else {
				break
			}
		}
	}
}
