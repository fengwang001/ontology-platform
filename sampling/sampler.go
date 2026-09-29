// Package sampling 实现按键哈希的一致采样（consistent key-based sampling）。
//
// 同一键的全部事件要么全部采出、要么全部丢弃；多个实例对同一 (键, 采样率)
// 的判定一致且可复现；调高采样率时已采键继续被采（单调性）。
package sampling

import (
	"log/slog"
	"sort"
	"sync"
)

// BucketCount 固定桶范围：桶值取值区间为 [0, BucketCount)。
// 采样率以整数万分比表示，取值区间为 [0, BucketCount]。
const BucketCount = 10000

// DefaultMaxKnownKeys 默认可记录的已知键上限。
const DefaultMaxKnownKeys = 1_000_000

// Event 是变更事件流中的一条事件，Key 为采样所依据的键。
type Event struct {
	Key   string
	Value any
}

// Sampler 是并发安全的一致采样器。
//
// 多个执行体（goroutine）可并发调用 Feed / SetRate / Sample / SelfCheck，
// 所有状态变更在互斥保护下进行，非法输入整体拒绝、失败不留痕。
type Sampler struct {
	mu           sync.Mutex
	rate         int
	known        map[string]int
	maxKnownKeys int
	logger       *slog.Logger
}

// New 创建采样器。rate 为初始万分比采样率，取值 [0, BucketCount]；
// maxKnownKeys <= 0 时使用 DefaultMaxKnownKeys。
func New(rate int, maxKnownKeys int, logger *slog.Logger) (*Sampler, error) {
	if rate < 0 || rate > BucketCount {
		return nil, newError(ErrRateOutOfRange,
			"sampling: rate out of range [0, 10000]: "+itoa(rate))
	}
	if maxKnownKeys <= 0 {
		maxKnownKeys = DefaultMaxKnownKeys
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Sampler{
		rate:         rate,
		known:        make(map[string]int),
		maxKnownKeys: maxKnownKeys,
		logger:       logger,
	}, nil
}

// Feed 按事件原顺序返回被采样的事件，并把其中出现过的键记为已知键。
// 同一批输入中若存在非法键或会导致已知键超限，整体拒绝、不留痕。
func (s *Sampler) Feed(events []Event) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.logger.Info("sampling.Feed begin", "rate", s.rate, "events", len(events))

	// 第一阶段：纯校验，任何非法输入都在状态变更前整体拒绝。
	seen := make(map[string]struct{}, len(events))
	newCount := 0
	for i := range events {
		key := events[i].Key
		if key == "" {
			s.logger.Warn("sampling.Feed rejected: empty key", "index", i)
			return nil, newError(ErrEmptyKey, "sampling: empty key at index "+itoa(i))
		}
		if _, dupInBatch := seen[key]; dupInBatch {
			continue
		}
		seen[key] = struct{}{}
		if _, known := s.known[key]; !known {
			newCount++
		}
	}
	if len(s.known)+newCount > s.maxKnownKeys {
		s.logger.Warn("sampling.Feed rejected: too many known keys",
			"known", len(s.known), "new", newCount, "limit", s.maxKnownKeys)
		return nil, newError(ErrTooManyKnownKeys,
			"sampling: known key count "+itoa(len(s.known)+newCount)+
				" exceeds limit "+itoa(s.maxKnownKeys))
	}

	// 第二阶段：提交。先登记新已知键，再按原顺序过滤输出。
	for key := range seen {
		if _, known := s.known[key]; !known {
			s.known[key] = Bucket(key)
		}
	}
	out := make([]Event, 0, len(events))
	for i := range events {
		key := events[i].Key
		bucket := s.known[key]
		keep := sampledByBucket(bucket, s.rate)
		s.logger.Info("sampling.Feed event",
			"index", i, "key", key, "bucket", bucket,
			"rate", s.rate, "sampled", keep,
			"reason", decideReason(bucket, s.rate))
		if keep {
			out = append(out, events[i])
		}
	}
	s.logger.Info("sampling.Feed end",
		"known", len(s.known), "kept", len(out), "dropped", len(events)-len(out))
	return out, nil
}

// RateChange 描述一次采样率调整对已知键集合的影响。
type RateChange struct {
	// Added 为新纳入的已知键（旧率下不可见、新率下可见），按 (桶值, 键) 排序。
	Added []string
	// Removed 为被移出的已知键（旧率下可见、新率下不可见），按 (桶值, 键) 排序。
	Removed []string
}

// SetRate 调整采样率，返回新纳入与被移出的已知键列表。
// 采样率非法或与当前相等时不改变任何状态。
func (s *Sampler) SetRate(rate int) (RateChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.logger.Info("sampling.SetRate begin", "old_rate", s.rate, "new_rate", rate)

	if rate < 0 || rate > BucketCount {
		s.logger.Warn("sampling.SetRate rejected: rate out of range", "rate", rate)
		return RateChange{}, newError(ErrRateOutOfRange,
			"sampling: rate out of range [0, 10000]: "+itoa(rate))
	}
	if rate == s.rate {
		// 采样率不变：两份列表都为空，且不改变任何状态。
		s.logger.Info("sampling.SetRate unchanged", "rate", rate)
		return RateChange{Added: []string{}, Removed: []string{}}, nil
	}

	added := make([]string, 0)
	removed := make([]string, 0)
	for key, bucket := range s.known {
		was := sampledByBucket(bucket, s.rate)
		now := sampledByBucket(bucket, rate)
		switch {
		case !was && now:
			added = append(added, key)
		case was && !now:
			removed = append(removed, key)
		}
	}
	sort.Slice(added, func(i, j int) bool {
		bi, bj := s.known[added[i]], s.known[added[j]]
		if bi != bj {
			return bi < bj
		}
		return added[i] < added[j]
	})
	sort.Slice(removed, func(i, j int) bool {
		bi, bj := s.known[removed[i]], s.known[removed[j]]
		if bi != bj {
			return bi < bj
		}
		return removed[i] < removed[j]
	})

	oldRate := s.rate
	s.rate = rate
	s.logger.Info("sampling.SetRate committed",
		"old_rate", oldRate, "new_rate", rate,
		"added", len(added), "removed", len(removed))
	return RateChange{Added: added, Removed: removed}, nil
}

// Sample 判定某键在当前采样率下是否应被采出；该调用不改变已知键集合。
func (s *Sampler) Sample(key string) (bool, error) {
	if key == "" {
		return false, newError(ErrEmptyKey, "sampling: empty key")
	}
	s.mu.Lock()
	rate := s.rate
	s.mu.Unlock()

	bucket := Bucket(key)
	keep := sampledByBucket(bucket, rate)
	s.logger.Info("sampling.Sample",
		"key", key, "bucket", bucket, "rate", rate,
		"sampled", keep, "reason", decideReason(bucket, rate))
	return keep, nil
}

// SelfCheck 校验内部不变量：已知键桶值与哈希规则一致、采样集合与当前率一致。
func (s *Sampler) SelfCheck() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.rate < 0 || s.rate > BucketCount {
		return newError(ErrRateOutOfRange,
			"sampling: self-check found illegal rate "+itoa(s.rate))
	}
	if len(s.known) > s.maxKnownKeys {
		return newError(ErrTooManyKnownKeys,
			"sampling: self-check found "+itoa(len(s.known))+
				" known keys, limit "+itoa(s.maxKnownKeys))
	}
	for key, bucket := range s.known {
		if want := Bucket(key); want != bucket {
			return newError(ErrNone,
				"sampling: self-check bucket mismatch for key "+key+
					": stored "+itoa(bucket)+", recomputed "+itoa(want))
		}
		if bucket < 0 || bucket >= BucketCount {
			return newError(ErrNone,
				"sampling: self-check bucket out of range for key "+key+": "+itoa(bucket))
		}
	}
	s.logger.Info("sampling.SelfCheck ok", "rate", s.rate, "known", len(s.known))
	return nil
}

// Rate 返回当前采样率。
func (s *Sampler) Rate() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rate
}

// KnownKeys 返回已知键数量。
func (s *Sampler) KnownKeys() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.known)
}

func decideReason(bucket, rate int) string {
	if bucket < rate {
		return "bucket < rate => sampled"
	}
	return "bucket >= rate => dropped"
}

func itoa(n int) string {
	// 避免仅为错误信息引入 strconv 的额外包装；标准库实现足够简单直接。
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [24]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
