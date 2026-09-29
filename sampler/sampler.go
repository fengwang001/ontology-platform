// Package sampler 提供按键哈希的一致采样：同一键的事件要么全部采出、
// 要么全部丢弃，多个实例对同一键的判定一致，且采样率调高时已采键继续被采。
package sampler

import (
	"errors"
	"hash/fnv"
	"sort"
	"sync"
)

const (
	// BucketCount 是哈希桶的总数，桶值范围为 [0, BucketCount)。
	BucketCount = 10000
	// MaxRate 是采样率上限（整数万分比），取值范围为 [0, MaxRate]。
	MaxRate = BucketCount
)

var (
	// ErrRateOutOfRange 表示采样率越界（不在 [0, MaxRate] 内）。
	ErrRateOutOfRange = errors.New("sampler: sample rate out of range [0, 10000]")
	// ErrEmptyKey 表示事件或查询携带了空键。
	ErrEmptyKey = errors.New("sampler: empty key")
	// ErrTooManyKnownKeys 表示已知键数量将超过上限。
	ErrTooManyKnownKeys = errors.New("sampler: known keys limit exceeded")
)

// Event 是变更事件流中的一条事件。
type Event struct {
	Key   string
	Value any
}

// KeyBucket 记录一个已知键及其哈希桶值，用于调整采样率时的差异报告。
type KeyBucket struct {
	Key    string
	Bucket uint32
}

// Sampler 是按键哈希的一致采样器，所有方法均可被多个执行体并发调用。
type Sampler struct {
	mu       sync.RWMutex
	rate     uint32
	maxKnown int
	known    map[string]uint32
}

// Bucket 返回键经 FNV-1a 64 位哈希后映射到的桶值，范围为 [0, BucketCount)。
// 该函数是纯函数，任意实例、任意进程对同一键返回相同结果。
func Bucket(key string) uint32 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return uint32(h.Sum64() % BucketCount)
}

// NewSampler 创建采样器。rate 为整数万分比，maxKnownKeys 为已知键数量上限。
func NewSampler(rate int, maxKnownKeys int) (*Sampler, error) {
	if rate < 0 || rate > MaxRate {
		return nil, ErrRateOutOfRange
	}
	return &Sampler{
		rate:     uint32(rate),
		maxKnown: maxKnownKeys,
		known:    make(map[string]uint32),
	}, nil
}

// Feed 喂入一批事件，返回被采出的事件（保持原顺序）。
// 批次内所有出现过的键会被记为已知键；任一输入非法时整批拒绝，状态不变。
func (s *Sampler) Feed(events []Event) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	newKeys := make(map[string]uint32)
	for _, ev := range events {
		if ev.Key == "" {
			return nil, ErrEmptyKey
		}
		if _, ok := s.known[ev.Key]; !ok {
			if _, ok := newKeys[ev.Key]; !ok {
				newKeys[ev.Key] = Bucket(ev.Key)
			}
		}
	}
	if s.maxKnown >= 0 && len(s.known)+len(newKeys) > s.maxKnown {
		return nil, ErrTooManyKnownKeys
	}
	for k, b := range newKeys {
		s.known[k] = b
	}

	sampled := make([]Event, 0, len(events))
	for _, ev := range events {
		if s.known[ev.Key] < s.rate {
			sampled = append(sampled, ev)
		}
	}
	return sampled, nil
}

// SetRate 调整采样率，返回新纳入与被移出的已知键列表（按桶值升序、同桶按键排序）。
// 采样率不变时两份列表都为空；rate 越界时整体拒绝，状态不变。
func (s *Sampler) SetRate(rate int) (added []KeyBucket, removed []KeyBucket, err error) {
	if rate < 0 || rate > MaxRate {
		return nil, nil, ErrRateOutOfRange
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	old := s.rate
	s.rate = uint32(rate)
	added = make([]KeyBucket, 0)
	removed = make([]KeyBucket, 0)
	for k, b := range s.known {
		switch {
		case b >= old && b < uint32(rate):
			added = append(added, KeyBucket{Key: k, Bucket: b})
		case b >= uint32(rate) && b < old:
			removed = append(removed, KeyBucket{Key: k, Bucket: b})
		}
	}
	sortKeyBuckets(added)
	sortKeyBuckets(removed)
	return added, removed, nil
}

// IsSampled 判定给定键在当前采样率下是否被采出，不修改任何状态。
func (s *Sampler) IsSampled(key string) (bool, error) {
	if key == "" {
		return false, ErrEmptyKey
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Bucket(key) < s.rate, nil
}

// Rate 返回当前采样率。
func (s *Sampler) Rate() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return int(s.rate)
}

// KnownKeys 返回当前已知键及其桶值，按桶值升序、同桶按键排序。
func (s *Sampler) KnownKeys() []KeyBucket {
	s.mu.RLock()
	defer s.mu.RUnlock()
	kbs := make([]KeyBucket, 0, len(s.known))
	for k, b := range s.known {
		kbs = append(kbs, KeyBucket{Key: k, Bucket: b})
	}
	sortKeyBuckets(kbs)
	return kbs
}

// SelfCheck 校验内部不变量：采样率合法、已知键数未超限、各键桶值与哈希一致。
func (s *Sampler) SelfCheck() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.rate > MaxRate {
		return ErrRateOutOfRange
	}
	if s.maxKnown >= 0 && len(s.known) > s.maxKnown {
		return ErrTooManyKnownKeys
	}
	for k, b := range s.known {
		if k == "" {
			return ErrEmptyKey
		}
		if b != Bucket(k) {
			return errors.New("sampler: bucket mismatch for key " + k)
		}
	}
	return nil
}

// sortKeyBuckets 按桶值升序、同桶按键字典序排序。
func sortKeyBuckets(kbs []KeyBucket) {
	sort.Slice(kbs, func(i, j int) bool {
		if kbs[i].Bucket != kbs[j].Bucket {
			return kbs[i].Bucket < kbs[j].Bucket
		}
		return kbs[i].Key < kbs[j].Key
	})
}
