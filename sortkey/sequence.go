package sortkey

import (
	"fmt"
	"slices"
	"sort"
	"sync"
)

// Entry 是序列中的一个元素：排序键 + 元素内容。
type Entry struct {
	Key   string
	Value string
}

// Stats 报告键长度使用情况。
type Stats struct {
	Longest  int // 当前最长键的长度
	Limit    int // 声明的键长度上限
	Headroom int // 距离上限还剩多少（Limit - Longest）
}

// Sequence 维护一组按排序键有序的元素，支持并发安全地插入、
// 读取与重排。生成规则由可插拔的 Generator 决定。
type Sequence struct {
	mu      sync.RWMutex
	gen     Generator
	entries []Entry // 始终按 Key 严格递增
}

// NewSequence 创建一个使用 gen 生成键的空序列。
func NewSequence(gen Generator) *Sequence {
	return &Sequence{gen: gen}
}

// Insert 在 left 与 right 之间插入 value，返回生成的新键。
// 空串表示该侧无邻居。当 left、right 之间已存在并发插入的
// 其他元素时，新元素按 value 的字典序落到确定的位置，
// 因此并发插入同一间隙的最终顺序与调度无关。
func (s *Sequence) Insert(left, right, value string) (string, error) {
	if err := s.gen.Validate(left); err != nil {
		return "", err
	}
	if err := s.gen.Validate(right); err != nil {
		return "", err
	}
	if right != "" && left >= right {
		return "", ErrInvalidOrder
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lo, hi := s.boundsLocked(left, right, value)
	key, err := s.gen.Between(lo, hi)
	if err != nil {
		return "", err
	}
	idx := sort.Search(len(s.entries), func(i int) bool {
		return s.entries[i].Key >= key
	})
	s.entries = slices.Insert(s.entries, idx, Entry{Key: key, Value: value})
	return key, nil
}

// boundsLocked 在 (left, right) 间隙内按 value 字典序找到新元素
// 的实际左右邻居。调用方必须已持有写锁。
func (s *Sequence) boundsLocked(left, right, value string) (lo, hi string) {
	lo, hi = left, right
	for _, e := range s.entries {
		if left != "" && e.Key <= left {
			continue
		}
		if right != "" && e.Key >= right {
			break
		}
		// 间隙内的元素按 value 有序（归纳不变量）。
		if e.Value < value {
			lo = e.Key
		} else {
			hi = e.Key
			break
		}
	}
	return lo, hi
}

// Rebalance 把所有元素重新分配为等间距的短键。
// 重排保持相对顺序不变；键的替换在写锁内一次性完成，
// 要么整体生效要么完全不生效，并发读取不会看到新旧混合。
func (s *Sequence) Rebalance() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys, err := s.gen.EvenKeys(len(s.entries))
	if err != nil {
		return err // 未做任何修改
	}
	next := make([]Entry, len(s.entries))
	for i, e := range s.entries {
		next[i] = Entry{Key: keys[i], Value: e.Value}
	}
	s.entries = next
	return nil
}

// Snapshot 返回当前序列的独立副本，供并发读取。
func (s *Sequence) Snapshot() []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.entries)
}

// Stats 报告当前最长键长度、上限与余量。
func (s *Sequence) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	longest := 0
	for _, e := range s.entries {
		longest = max(longest, len(e.Key))
	}
	limit := s.gen.Limit()
	return Stats{Longest: longest, Limit: limit, Headroom: limit - longest}
}

// SelfCheck 验证所有键合法、严格递增且无重复、长度未超限。
// 可被测试直接调用。
func (s *Sequence) SelfCheck() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i, e := range s.entries {
		if err := s.gen.Validate(e.Key); err != nil {
			return err
		}
		if len(e.Key) > s.gen.Limit() {
			return fmt.Errorf("sortkey: key %q exceeds length limit %d", e.Key, s.gen.Limit())
		}
		if i > 0 && s.entries[i-1].Key >= e.Key {
			return fmt.Errorf("sortkey: keys not strictly increasing at index %d: %q >= %q",
				i, s.entries[i-1].Key, e.Key)
		}
	}
	return nil
}
