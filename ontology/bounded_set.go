// Package ontology 提供本体服务平台的基础构件。
package ontology

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// 可区分的失败原因，调用方可用 errors.Is 判定。
var (
	// ErrInvalidCapacity 容量非法（<= 0），构造时整体拒绝。
	ErrInvalidCapacity = errors.New("bounded set: invalid capacity")
	// ErrEmptyKey 键为空，跟踪请求整体拒绝。
	ErrEmptyKey = errors.New("bounded set: empty key")
	// ErrNegativeTimestamp 活动时间戳为负，跟踪请求整体拒绝。
	ErrNegativeTimestamp = errors.New("bounded set: negative timestamp")
)

// KeyState 是单个键在集合中的确定性状态快照。
type KeyState struct {
	Key       string // 键
	Timestamp int64  // 最近活动时间戳
	Slot      uint64 // 首次进入时分配、驱逐后不复用的位点
}

// BoundedSet 是按键最近活跃度有界蓄存的集合。
// 超出容量时确定性地驱逐 (活动时间戳, 位点) 最小者。
// 所有方法均可并发调用。
type BoundedSet struct {
	mu        sync.Mutex
	capacity  int
	entries   map[string]KeyState
	nextSlot  uint64
	evictions uint64
}

// NewBoundedSet 创建容量为 capacity 的有界集合。
// capacity <= 0 时返回 ErrInvalidCapacity，不创建任何状态。
func NewBoundedSet(capacity int) (*BoundedSet, error) {
	if capacity <= 0 {
		return nil, fmt.Errorf("%w: %d", ErrInvalidCapacity, capacity)
	}
	return &BoundedSet{capacity: capacity, entries: make(map[string]KeyState)}, nil
}

// Capacity 返回集合容量上限。
func (s *BoundedSet) Capacity() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.capacity
}

// Track 跟踪一个键。
// 已存在的键只刷新活动时间戳，集合大小不变；
// 已被驱逐的键按新键处理，分配新的不复用位点。
// 参数非法时整体拒绝，集合、时间戳、位点分配与计数均不变。
func (s *BoundedSet) Track(key string, ts int64) error {
	if key == "" {
		return ErrEmptyKey
	}
	if ts < 0 {
		return fmt.Errorf("%w: %d", ErrNegativeTimestamp, ts)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if st, ok := s.entries[key]; ok {
		st.Timestamp = ts
		s.entries[key] = st
		return nil
	}

	st := KeyState{Key: key, Timestamp: ts, Slot: s.nextSlot}
	s.nextSlot++
	s.entries[key] = st

	if len(s.entries) > s.capacity {
		victim := s.victimLocked()
		delete(s.entries, victim.Key)
		s.evictions++
	}
	return nil
}

// victimLocked 返回驱逐受害者：活动时间戳最小者，并列时位点较小者。
// 调用方必须持有锁且集合非空。
func (s *BoundedSet) victimLocked() KeyState {
	first := true
	var victim KeyState
	for _, st := range s.entries {
		if first || st.Timestamp < victim.Timestamp ||
			(st.Timestamp == victim.Timestamp && st.Slot < victim.Slot) {
			victim = st
			first = false
		}
	}
	return victim
}

// Count 返回当前集合大小，恒等于实际成员数且永不超过容量。
func (s *BoundedSet) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// Evictions 返回累计驱逐次数。
func (s *BoundedSet) Evictions() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.evictions
}

// Snapshot 返回当前成员状态的确定性快照：
// 按 (活动时间戳, 位点) 升序排列，与驱逐判定序一致。
// 快照在锁内一次性拷贝，并发读不会看到内部中间态。
func (s *BoundedSet) Snapshot() []KeyState {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]KeyState, 0, len(s.entries))
	for _, st := range s.entries {
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Timestamp != out[j].Timestamp {
			return out[i].Timestamp < out[j].Timestamp
		}
		return out[i].Slot < out[j].Slot
	})
	return out
}

// SelfCheck 校验内部不变量：大小不超过容量、位点唯一且不复用、
// 位点分配计数与驱逐计数自洽。全部通过返回 nil。
func (s *BoundedSet) SelfCheck() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.capacity <= 0 {
		return fmt.Errorf("selfcheck: capacity %d is not positive", s.capacity)
	}
	if len(s.entries) > s.capacity {
		return fmt.Errorf("selfcheck: size %d exceeds capacity %d", len(s.entries), s.capacity)
	}
	seen := make(map[uint64]string, len(s.entries))
	var maxSlot uint64
	for key, st := range s.entries {
		if st.Key != key {
			return fmt.Errorf("selfcheck: entry key mismatch %q vs %q", key, st.Key)
		}
		if st.Timestamp < 0 {
			return fmt.Errorf("selfcheck: key %q has negative timestamp %d", key, st.Timestamp)
		}
		if prev, dup := seen[st.Slot]; dup {
			return fmt.Errorf("selfcheck: slot %d reused by %q and %q", st.Slot, prev, key)
		}
		seen[st.Slot] = key
		if st.Slot > maxSlot {
			maxSlot = st.Slot
		}
	}
	if len(s.entries) > 0 && maxSlot >= s.nextSlot {
		return fmt.Errorf("selfcheck: slot %d not below nextSlot %d", maxSlot, s.nextSlot)
	}
	if s.nextSlot < uint64(len(s.entries))+s.evictions {
		return fmt.Errorf("selfcheck: nextSlot %d inconsistent with size %d and evictions %d",
			s.nextSlot, len(s.entries), s.evictions)
	}
	return nil
}
