// Package tophits 维护最近 N 条键值变更的计数型滑动窗口，
// 并按窗口内分值之和维护前 K 高分键。
package tophits

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	// ErrNonPositiveWindow 表示窗口容量 N 不是正整数。
	ErrNonPositiveWindow = errors.New("tophits: window size must be positive")
	// ErrNonPositiveK 表示前 K 数不是正整数。
	ErrNonPositiveK = errors.New("tophits: top-k must be positive")
	// ErrKExceedsWindow 表示前 K 数超过窗口容量。
	ErrKExceedsWindow = errors.New("tophits: top-k must not exceed window size")
	// ErrEmptyKey 表示某条变更的键为空字符串。
	ErrEmptyKey = errors.New("tophits: change key must not be empty")
)

// Change 表示一次键分值变更。
type Change struct {
	Key   string
	Score int64
}

// Entry 表示一个键在当前窗口内的汇总结果。
type Entry struct {
	Key   string
	Score int64
}

// SlidingTopK 是并发安全的计数型滑动窗口前 K 维护结构。
type SlidingTopK struct {
	mu sync.RWMutex

	n int
	k int

	// ring 存放最近至多 n 条变更，ring[head] 为最旧。
	ring []Change
	// head 指向最旧变更；size 为当前窗口内变更条数。
	head int
	size int

	// scores[key] 为键在窗口内的分值之和。
	scores map[string]int64
	// counts[key] 为键在窗口内的变更条数，归零即删除该键。
	counts map[string]int
}

// NewSlidingTopK 创建一个容量为 n、保留前 k 名的滑动窗口。
func NewSlidingTopK(n, k int) (*SlidingTopK, error) {
	if n <= 0 {
		return nil, ErrNonPositiveWindow
	}
	if k <= 0 {
		return nil, ErrNonPositiveK
	}
	if k > n {
		return nil, ErrKExceedsWindow
	}
	return &SlidingTopK{
		n:      n,
		k:      k,
		ring:   make([]Change, 0, n),
		scores: make(map[string]int64),
		counts: make(map[string]int),
	}, nil
}

// Apply 原子地应用一整批变更；任一条非法则整批拒绝。
func (s *SlidingTopK) Apply(changes []Change) error {
	for i := range changes {
		if changes[i].Key == "" {
			return fmt.Errorf("%w: change index %d", ErrEmptyKey, i)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range changes {
		s.add(changes[i])
	}
	return nil
}

// add 追加一条变更，并在窗口已满时滑出最旧变更。
// 调用方必须持有写锁。
func (s *SlidingTopK) add(c Change) {
	if s.size == s.n {
		oldest := s.ring[s.head]
		s.subtract(oldest)
		s.ring[s.head] = c
		s.head = (s.head + 1) % s.n
	} else {
		s.ring = append(s.ring, c)
		s.size++
	}
	s.scores[c.Key] += c.Score
	s.counts[c.Key]++
}

// subtract 撤回一条已滑出窗口的变更对汇总值的贡献。
// 调用方必须持有写锁。
func (s *SlidingTopK) subtract(c Change) {
	s.scores[c.Key] -= c.Score
	s.counts[c.Key]--
	if s.counts[c.Key] <= 0 {
		delete(s.scores, c.Key)
		delete(s.counts, c.Key)
	}
}

// orderedEntries 在调用方持有的锁保护下收集全部存活键并排序。
func (s *SlidingTopK) orderedEntries() []Entry {
	entries := make([]Entry, 0, len(s.scores))
	for key, score := range s.scores {
		entries = append(entries, Entry{Key: key, Score: score})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Score != entries[j].Score {
			return entries[i].Score > entries[j].Score
		}
		return entries[i].Key < entries[j].Key
	})
	return entries
}

// TopK 返回当前窗口内分值最高的前 K 个键的快照副本。
func (s *SlidingTopK) TopK() []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries := s.orderedEntries()
	if len(entries) > s.k {
		entries = entries[:s.k]
	}
	return entries
}

// Snapshot 返回窗口内全部存活键，按 TopK 相同顺序排列的快照副本。
func (s *SlidingTopK) Snapshot() []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.orderedEntries()
}

// SnapshotWithTopK 在同一把读锁内返回全部存活键快照及其前 K 名前缀，
// 保证两者来自同一个窗口状态，适合并发自检场景。
func (s *SlidingTopK) SnapshotWithTopK() (all []Entry, top []Entry) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	all = s.orderedEntries()
	if len(all) > s.k {
		top = all[:s.k]
	} else {
		top = all
	}
	return all, top
}

// SelfCheck 复核内部不变量并在被破坏时返回描述性错误。
func (s *SlidingTopK) SelfCheck() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.n <= 0 || s.k <= 0 || s.k > s.n {
		return fmt.Errorf("tophits: invalid parameters n=%d k=%d", s.n, s.k)
	}
	if s.size < 0 || s.size > s.n {
		return fmt.Errorf("tophits: window size %d out of range [0,%d]", s.size, s.n)
	}
	if s.head < 0 || s.head >= s.n {
		return fmt.Errorf("tophits: ring head %d out of range [0,%d)", s.head, s.n)
	}
	if len(s.scores) != len(s.counts) {
		return fmt.Errorf("tophits: scores map has %d keys but counts map has %d", len(s.scores), len(s.counts))
	}

	wantScores := make(map[string]int64, len(s.counts))
	wantCounts := make(map[string]int, len(s.counts))
	seenSlots := make([]bool, s.n)
	for i := 0; i < s.size; i++ {
		slot := (s.head + i) % s.n
		if seenSlots[slot] {
			return fmt.Errorf("tophits: ring slot %d visited twice", slot)
		}
		seenSlots[slot] = true
		c := s.ring[slot]
		if c.Key == "" {
			return fmt.Errorf("tophits: ring slot %d holds empty key", slot)
		}
		wantScores[c.Key] += c.Score
		wantCounts[c.Key]++
	}

	for key, count := range s.counts {
		if count <= 0 {
			return fmt.Errorf("tophits: non-positive count %d for key %q", count, key)
		}
		if wantCounts[key] != count {
			return fmt.Errorf("tophits: count mismatch for key %q: maintained=%d recomputed=%d", key, count, wantCounts[key])
		}
	}
	for key, score := range s.scores {
		if wantScores[key] != score {
			return fmt.Errorf("tophits: score mismatch for key %q: maintained=%d recomputed=%d", key, score, wantScores[key])
		}
	}
	for key := range wantScores {
		if _, ok := s.scores[key]; !ok {
			return fmt.Errorf("tophits: live key %q missing from scores map", key)
		}
	}
	return nil
}

// WindowSize 返回窗口容量 N。
func (s *SlidingTopK) WindowSize() int { return s.n }

// TopKSize 返回 K。
func (s *SlidingTopK) TopKSize() int { return s.k }

// Len 返回当前窗口内的变更条数。
func (s *SlidingTopK) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.size
}
