package snapshot

import (
	"errors"
	"sort"
	"sync"
	"sync/atomic"

	"ontology/store"
)

// ErrClosed 在快照关闭后仍被使用时返回。
var ErrClosed = errors.New("snapshot: snapshot closed")

// Snapshot 是某一时刻的只读快照句柄。
type Snapshot struct {
	st     *store.Store
	ver    uint64
	keys   []string
	closed atomic.Bool
	wg     sync.WaitGroup
	reads  atomic.Int64
}

// Take 在当前时刻建立快照并向 store 注册水位。
func Take(st *store.Store) *Snapshot {
	ver, keys := st.Take()
	st.Register(ver)
	return &Snapshot{st: st, ver: ver, keys: keys}
}

// Version 返回快照水位。
func (s *Snapshot) Version() uint64 { return s.ver }

// Keys 返回快照时刻的键集合（字典序）。
func (s *Snapshot) Keys() []string { return append([]string(nil), s.keys...) }

// Begin 声明一段导出操作；关闭后的快照拒绝新操作。
func (s *Snapshot) Begin() error {
	if s.closed.Load() {
		return ErrClosed
	}
	s.wg.Add(1)
	if s.closed.Load() {
		s.wg.Done()
		return ErrClosed
	}
	return nil
}

// End 结束一段导出操作。
func (s *Snapshot) End() { s.wg.Done() }

// Closed 报告快照是否已关闭。
func (s *Snapshot) Closed() bool { return s.closed.Load() }

// Get 读取快照时刻某键的值。ok=false 表示该键当时不存在。
func (s *Snapshot) Get(key string) (value []byte, present, ok bool) {
	s.reads.Add(1)
	return s.st.ReadAt(key, s.ver)
}

// Reads 返回本快照累计读取次数。
func (s *Snapshot) Reads() int64 { return s.reads.Load() }

// OrderedKeys 返回指定顺序的键：lex=true 字典序，否则逆序。
func (s *Snapshot) OrderedKeys(lex bool) []string {
	keys := s.Keys()
	if !lex {
		for i, j := 0, len(keys)-1; i < j; i, j = i+1, j-1 {
			keys[i], keys[j] = keys[j], keys[i]
		}
	}
	return keys
}

// Retained 返回 store 中为所有活跃快照保留的旧值条数。
func (s *Snapshot) Retained() int { return s.st.Retained() }

// Close 标记关闭，等待在途导出结束后释放本快照的旧值。
func (s *Snapshot) Close() {
	if !s.closed.CompareAndSwap(false, true) {
		return
	}
	s.wg.Wait()
	s.st.Unregister(s.ver)
}

// SortedKeys 是供外部复用的字典序工具。
func SortedKeys(keys []string) []string {
	out := append([]string(nil), keys...)
	sort.Strings(out)
	return out
}
