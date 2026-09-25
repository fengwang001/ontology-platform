// Package snap 实现每 Key 版本历史、按快照取值（二分定位）、快照登记与释放。依赖 ver。
package snap

import (
	"errors"
	"math/bits"
	"sync"
	"sync/atomic"

	"ontology/ver"
)

// 哨兵错误，互不相同，供 errors.Is 判定。
var (
	ErrMaxSnapshots     = errors.New("snap: maxSnapshots must be positive")
	ErrEmptyKey         = errors.New("snap: empty key")
	ErrInvalidSnapshot  = errors.New("snap: invalid snapshot id")
	ErrTooManySnapshots = errors.New("snap: too many live snapshots")
	ErrSnapshotNotFound = errors.New("snap: snapshot not registered")
	errLinearScan       = errors.New("snap: read checks grow linearly")
)

type entry struct {
	ver int
	val int64
}

// Store 是进程内存中的多版本键值存储。读（Read/ReadCurrent）不修改 ver 与历史。
type Store struct {
	mu           sync.RWMutex
	cur          int // 全局版本号，仅 Write 递增
	hist         map[string][]entry
	live         map[int]int // 存活快照 id -> 登记次数
	maxSnapshots int
	checked      atomic.Int64 // 最近一次 Read 检查的历史条目数；非导出，不进公开接口
}

// New 构造 Store；maxSnapshots 非正时整体失败。
func New(maxSnapshots int) (*Store, error) {
	if maxSnapshots <= 0 {
		return nil, ErrMaxSnapshots
	}
	return &Store{hist: map[string][]entry{}, live: map[int]int{}, maxSnapshots: maxSnapshots}, nil
}

// Write 使 ver 递增 1 并把 (ver, v) 追加到 key 的版本历史。空 Key 拒绝且不留痕。
func (s *Store) Write(key string, v int64) error {
	if key == "" {
		return ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cur++
	s.hist[key] = append(s.hist[key], entry{s.cur, v})
	return nil
}

// Snapshot 返回当前 ver 作为快照 id 并登记为存活；存活数超限则拒绝且不留痕。
func (s *Store) Snapshot() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.live {
		n += c
	}
	if n >= s.maxSnapshots {
		return 0, ErrTooManySnapshots
	}
	s.live[s.cur]++
	return s.cur, nil
}

// Read 返回 hist[key] 中 ver <= snap 的最大版本对应的值（二分定位，O(log m)）；无则 0。
func (s *Store) Read(snap int, key string) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !ver.ValidSnap(snap, s.cur) {
		return 0, ErrInvalidSnapshot
	}
	h := s.hist[key]
	lo, hi := 0, len(h)
	var n int64
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		n++
		if ver.Visible(snap, h[mid].ver) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	s.checked.Store(n)
	if lo == 0 {
		return 0, nil
	}
	return h[lo-1].val, nil
}

// ReadCurrent 返回最新值（历史末项，无则 0）。
func (s *Store) ReadCurrent(key string) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	h := s.hist[key]
	if len(h) == 0 {
		return 0
	}
	return h[len(h)-1].val
}

// Release 撤销一个快照的存活登记；id 非法或未登记则拒绝且不留痕。
func (s *Store) Release(snap int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !ver.ValidSnap(snap, s.cur) {
		return ErrInvalidSnapshot
	}
	c, ok := s.live[snap]
	if !ok {
		return ErrSnapshotNotFound
	}
	if c == 1 {
		delete(s.live, snap)
	} else {
		s.live[snap] = c - 1
	}
	return nil
}

// SelfCheck 核验 Read 的定位为次线性：对多档 m 写 m 个版本后在快照 1 处 Read，
// 检查条目数不得超过 log2(m)+小常数。只返回错误，计数器数值不出本包。
func SelfCheck() error {
	for _, m := range []int{100, 1000, 10000} {
		st, err := New(1)
		if err != nil {
			return err
		}
		for i := 0; i < m; i++ {
			if err := st.Write("k", int64(i)); err != nil {
				return err
			}
		}
		if _, err := st.Read(1, "k"); err != nil {
			return err
		}
		if st.checked.Load() > int64(bits.Len(uint(m)))+1 {
			return errLinearScan
		}
	}
	return nil
}
