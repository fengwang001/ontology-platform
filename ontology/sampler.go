package ontology

import (
	"hash/fnv"
	"sort"
	"sync"
)

const (
	// MaxRate 采样率上限，单位为万分比，10000 即 100%。
	MaxRate = 10000
	// DefaultMaxKnownKeys 默认已知键容量上限。
	DefaultMaxKnownKeys = 1_000_000
)

// Event 是变更事件流中的一条事件，Key 决定采样归属。
type Event[V any] struct {
	Key   string
	Value V
}

// Sampler 对变更事件按键做一致性哈希采样。
// 所有方法可被多个执行体并发调用；判定只依赖键、采样率与固定哈希函数。
type Sampler[V any] struct {
	mu           sync.RWMutex
	rate         int
	knownKeys    map[string]struct{}
	maxKnownKeys int
	logf         func(format string, args ...any)
}

// Option 配置 Sampler。
type Option[V any] func(*Sampler[V])

// WithMaxKnownKeys 设置已知键容量上限，必须 > 0。
func WithMaxKnownKeys[V any](n int) Option[V] {
	return func(s *Sampler[V]) { s.maxKnownKeys = n }
}

// WithLogger 设置逐步日志输出函数，每步打印输入、桶值与判定依据。
func WithLogger[V any](logf func(format string, args ...any)) Option[V] {
	return func(s *Sampler[V]) { s.logf = logf }
}

// NewSampler 创建采样器，初始采样率 initialRate 必须在 [0, MaxRate] 内。
func NewSampler[V any](initialRate int, opts ...Option[V]) (*Sampler[V], *RejectError) {
	if initialRate < 0 || initialRate > MaxRate {
		return nil, &RejectError{Reason: ReasonRateOutOfRange}
	}
	s := &Sampler[V]{
		rate:         initialRate,
		knownKeys:    make(map[string]struct{}),
		maxKnownKeys: DefaultMaxKnownKeys,
		logf:         func(string, ...any) {},
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.maxKnownKeys <= 0 {
		return nil, &RejectError{Reason: ReasonTooManyKnownKeys}
	}
	s.logf("new sampler: rate=%d/%d (%.4f%%)",
		initialRate, MaxRate, float64(initialRate)*100/MaxRate)
	return s, nil
}

// HashBucket 返回键的桶值：FNV-1a 64 位哈希对 MaxRate 取模，范围 [0, MaxRate)。
// FNV-1a 为确定性哈希，不随进程、实例或运行次数变化，因此跨实例判定一致、可复现。
func HashBucket(key string) int {
	h := fnv.New64a()
	h.Write([]byte(key))
	return int(h.Sum64() % uint64(MaxRate))
}

// BucketSampled 是朴素判定：桶值严格小于采样率时采出。
func BucketSampled(bucket, rate int) bool {
	return bucket >= 0 && bucket < MaxRate && bucket < rate
}

// Feed 喂入一批事件，返回被采出的事件并保持原顺序，所有键记为已知键。
// 整批先做完整校验：任一事件空键、或接受后已知键会超限，则整体拒绝、不留痕。
func (s *Sampler[V]) Feed(events []Event[V]) ([]Event[V], *RejectError) {
	s.mu.Lock()
	defer s.mu.Unlock()

	newKeys := 0
	seen := make(map[string]struct{}, len(events))
	for i, ev := range events {
		if ev.Key == "" {
			s.logf("feed rejected at index=%d: empty key (state unchanged, rate=%d, known=%d)",
				i, s.rate, len(s.knownKeys))
			return nil, &RejectError{Reason: ReasonEmptyKey}
		}
		if _, known := s.knownKeys[ev.Key]; !known {
			if _, dup := seen[ev.Key]; !dup {
				seen[ev.Key] = struct{}{}
				newKeys++
			}
		}
	}
	if len(s.knownKeys)+newKeys > s.maxKnownKeys {
		s.logf("feed rejected: known %d + new %d > max %d (state unchanged, rate=%d)",
			len(s.knownKeys), newKeys, s.maxKnownKeys, s.rate)
		return nil, &RejectError{Reason: ReasonTooManyKnownKeys}
	}

	kept := make([]Event[V], 0, len(events))
	for _, ev := range events {
		bucket := HashBucket(ev.Key)
		sampled := BucketSampled(bucket, s.rate)
		s.logf("feed key=%q bucket=%d rate=%d sampled=%t (bucket < rate => %t)",
			ev.Key, bucket, s.rate, sampled, bucket < s.rate)
		if _, known := s.knownKeys[ev.Key]; !known {
			s.knownKeys[ev.Key] = struct{}{}
		}
		if sampled {
			kept = append(kept, ev)
		}
	}
	s.logf("feed done: in=%d kept=%d known=%d rate=%d",
		len(events), len(kept), len(s.knownKeys), s.rate)
	return kept, nil
}

// RateChange 描述一次采样率调整的结果。
type RateChange struct {
	OldRate int
	NewRate int
	Added   []string
	Removed []string
}

// AdjustRate 调整采样率，返回新纳入与被移出的已知键（按桶值、键排序）。
// 采样率非法时整体拒绝、状态不变；采样率不变时两份列表均为空。
func (s *Sampler[V]) AdjustRate(newRate int) (RateChange, *RejectError) {
	if newRate < 0 || newRate > MaxRate {
		s.logf("adjust rejected: rate=%d out of [0,%d] (state unchanged)", newRate, MaxRate)
		return RateChange{}, &RejectError{Reason: ReasonRateOutOfRange}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	oldRate := s.rate
	change := RateChange{
		OldRate: oldRate,
		NewRate: newRate,
		Added:   []string{},
		Removed: []string{},
	}
	if oldRate == newRate {
		s.logf("adjust no-op: rate unchanged at %d, added=0 removed=0", newRate)
		return change, nil
	}

	for key := range s.knownKeys {
		bucket := HashBucket(key)
		wasSampled := bucket < oldRate
		nowSampled := bucket < newRate
		switch {
		case !wasSampled && nowSampled:
			change.Added = append(change.Added, key)
		case wasSampled && !nowSampled:
			change.Removed = append(change.Removed, key)
		}
	}
	sortByBucketThenKey(change.Added)
	sortByBucketThenKey(change.Removed)

	s.logf("adjust rate %d -> %d: added=%d removed=%d (each key re-checked by bucket < rate)",
		oldRate, newRate, len(change.Added), len(change.Removed))
	for _, key := range change.Added {
		s.logf("  added   key=%q bucket=%d", key, HashBucket(key))
	}
	for _, key := range change.Removed {
		s.logf("  removed key=%q bucket=%d", key, HashBucket(key))
	}

	s.rate = newRate
	return change, nil
}

// ShouldSample 判定单个键在当前采样率下是否被采出，空键被拒绝。
// 判定不登记已知键；可见性规则与 Feed 完全一致，同一键所有事件判定相同。
func (s *Sampler[V]) ShouldSample(key string) (bool, *RejectError) {
	if key == "" {
		s.logf("should-sample rejected: empty key (state unchanged)")
		return false, &RejectError{Reason: ReasonEmptyKey}
	}
	s.mu.RLock()
	rate := s.rate
	s.mu.RUnlock()
	bucket := HashBucket(key)
	sampled := BucketSampled(bucket, rate)
	s.logf("should-sample key=%q bucket=%d rate=%d sampled=%t (bucket < rate => %t)",
		key, bucket, rate, sampled, bucket < rate)
	return sampled, nil
}

// Rate 返回当前采样率（万分比）。
func (s *Sampler[V]) Rate() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rate
}

// KnownKeys 返回已知键数量。
func (s *Sampler[V]) KnownKeys() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.knownKeys)
}

// SelfCheck 自检不变量：采样率范围、每个已知键桶值范围与朴素判定一致性、单调性边界。
func (s *Sampler[V]) SelfCheck() error {
	s.mu.RLock()
	rate := s.rate
	known := make([]string, 0, len(s.knownKeys))
	for key := range s.knownKeys {
		known = append(known, key)
	}
	s.mu.RUnlock()

	if rate < 0 || rate > MaxRate {
		return &RejectError{Reason: ReasonRateOutOfRange}
	}
	for _, key := range known {
		bucket := HashBucket(key)
		if bucket < 0 || bucket >= MaxRate {
			s.logf("self-check failed: key=%q bucket=%d out of [0,%d)", key, bucket, MaxRate)
			return &RejectError{Reason: ReasonRateOutOfRange}
		}
		// 单调性边界：率为 0 时无键可见，率为 MaxRate 时全部可见。
		if BucketSampled(bucket, 0) {
			return &RejectError{Reason: ReasonRateOutOfRange}
		}
		if !BucketSampled(bucket, MaxRate) {
			return &RejectError{Reason: ReasonRateOutOfRange}
		}
		// 与朴素判定一致。
		if BucketSampled(bucket, rate) != (bucket < rate) {
			return &RejectError{Reason: ReasonRateOutOfRange}
		}
	}
	s.logf("self-check ok: rate=%d known=%d buckets in [0,%d), rule bucket<rate",
		rate, len(known), MaxRate)
	return nil
}

// sortByBucketThenKey 按桶值升序、同桶按键名字典序排序。
func sortByBucketThenKey(keys []string) {
	sort.Slice(keys, func(i, j int) bool {
		bi, bj := HashBucket(keys[i]), HashBucket(keys[j])
		if bi != bj {
			return bi < bj
		}
		return keys[i] < keys[j]
	})
}
