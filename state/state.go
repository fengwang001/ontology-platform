// Package state 是写者优先读写自旋锁的原始状态机与 O(1) 判定。不依赖其他包；先校验后变更，拒绝零写入。
package state

import (
	"errors"
	"sort"
	"sync"
)

// 六类可判定哨兵错误，互不相同。
var (
	ErrNotReader, ErrNotWriter              = errors.New("state: 未持读就释放"), errors.New("state: 未持写就释放")
	ErrReaderReentry, ErrWriterReentry      = errors.New("state: 读锁重入"), errors.New("state: 写锁重入")
	ErrUpgradeNotReader, ErrUpgradeConflict = errors.New("state: 未持读不能升级"), errors.New("state: 存在其他读者，升级冲突")
)

// Snapshot 是当前状态四元组，仅供测试与自检。Writer 无写者时为 -1。
type Snapshot struct {
	Readers        []int
	Writer         int
	WaitingWriters int
	WaitingReaders []int
}

type State struct {
	mu      sync.Mutex
	readers map[int]struct{}
	writer  int
	waitW   int // 等待写者计数；>0 即「有写者等待」
	waitR   map[int]struct{}
	probe   int // 最近一次自旋判定访问的字段数（非导出，不进公开接口）
}

func New() *State {
	return &State{readers: make(map[int]struct{}), writer: -1, waitR: make(map[int]struct{})}
}

// TryRead：写者优先体现在 waitW>0 时新读者不得获读。
func (s *State) TryRead(o int) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.probe = 3 // readers、writer、waitW，与读者总数 m 无关
	if _, ok := s.readers[o]; ok {
		return false, ErrReaderReentry
	}
	if s.writer >= 0 || s.waitW > 0 {
		return false, nil
	}
	s.readers[o] = struct{}{}
	return true, nil
}

// TryWrite：要求无写者且读者集合为空，只看 len 不遍历。
func (s *State) TryWrite(o int) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.probe = 2 // writer、readers
	if s.writer == o {
		return false, ErrWriterReentry
	}
	if s.writer >= 0 || len(s.readers) > 0 {
		return false, nil
	}
	s.writer = o
	return true, nil
}

func (s *State) ReleaseRead(o int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.probe = 1
	if _, ok := s.readers[o]; !ok {
		return ErrNotReader
	}
	delete(s.readers, o)
	return nil
}

func (s *State) ReleaseWrite(o int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.probe = 1
	if s.writer != o {
		return ErrNotWriter
	}
	s.writer = -1
	return nil
}

// Upgrade 原地读升写：必须已持读且唯一读者，否则可判定错误，不阻塞。
func (s *State) Upgrade(o int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.probe = 2
	if _, ok := s.readers[o]; !ok {
		return ErrUpgradeNotReader
	}
	if len(s.readers) != 1 {
		return ErrUpgradeConflict
	}
	delete(s.readers, o)
	s.writer = o
	return nil
}

func (s *State) WaitWriter(d int) { s.mu.Lock(); s.waitW += d; s.mu.Unlock() }

func (s *State) WaitReader(o int, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !on {
		delete(s.waitR, o)
		return
	}
	s.waitR[o] = struct{}{}
}

func (s *State) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := Snapshot{Writer: s.writer, WaitingWriters: s.waitW}
	for o := range s.readers {
		snap.Readers = append(snap.Readers, o)
	}
	for o := range s.waitR {
		snap.WaitingReaders = append(snap.WaitingReaders, o)
	}
	sort.Ints(snap.Readers)
	sort.Ints(snap.WaitingReaders)
	return snap
}

// CheckProbeBound 内部验证：m 个读者持读时每次自旋判定访问的字段数恒 ≤3（O(1)）；只返回布尔。
func CheckProbeBound() bool {
	for _, m := range []int{100, 1000, 10000} {
		s := New()
		for o := 0; o < m; o++ {
			if ok, _ := s.TryRead(o); !ok {
				return false
			}
		}
		if ok, _ := s.TryWrite(m); ok || s.probe > 3 {
			return false
		}
		if _, err := s.TryRead(0); err != ErrReaderReentry || s.probe > 3 {
			return false
		}
	}
	return true
}
