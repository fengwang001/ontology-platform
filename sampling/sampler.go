package sampling

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Event 是变更事件流中的一条事件，Key 为采样判定键。
type Event struct {
	Key     string
	Payload any
}

// Logger 记录每步输入、桶值与判定依据；nil 表示不记录。
type Logger interface {
	Printf(format string, args ...any)
}

// Option 配置采样器可选项。
type Option func(*Sampler)

// WithLogger 注入步骤日志记录器（记录输入、桶值与判定依据）。
func WithLogger(l Logger) Option {
	return func(s *Sampler) { s.log = l }
}

// DefaultMaxKnownKeys 是构造采样器时的默认已知键上限。
const DefaultMaxKnownKeys = 1_000_000

// Sampler 是并发安全的按键哈希一致采样器。
type Sampler struct {
	mu       sync.RWMutex
	rate     uint32
	maxKnown int // <=0 表示不限
	known    map[string]uint32
	log      Logger
}

// New 创建采样率为 rate（万分比）、已知键上限为 maxKnownKeys 的采样器。
// rate 取值范围 [0, BucketRange]；maxKnownKeys <= 0 表示不限制已知键数量。
func New(rate int, maxKnownKeys int, opts ...Option) (*Sampler, error) {
	if rate < 0 || rate > BucketRange {
		return nil, &InvalidInput{Reason: ErrRateOutOfRange}
	}
	s := &Sampler{
		rate:     uint32(rate),
		maxKnown: maxKnownKeys,
		known:    make(map[string]uint32),
	}
	for _, opt := range opts {
		opt(s)
	}
	s.logf("new sampler: rate=%d bucketRange=%d maxKnownKeys=%d", rate, BucketRange, maxKnownKeys)
	return s, nil
}

// Feed 喂入一批事件，返回其中被采样的事件，保持原顺序。
// 本批中出现过的所有键都会登记为已知键。只要存在空键或登记后已知键
// 数量超过上限，整批被拒绝：采样率与已知键集合保持不变（失败不留痕）。
func (s *Sampler) Feed(events []Event) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range events {
		if events[i].Key == "" {
			s.logf("feed rejected: event[%d] has empty key; rate=%d known=%d unchanged",
				i, s.rate, len(s.known))
			return nil, &InvalidInput{Reason: ErrEmptyKey}
		}
	}

	added := 0
	for i := range events {
		if _, ok := s.known[events[i].Key]; !ok {
			added++
		}
	}
	if s.maxKnown > 0 && len(s.known)+added > s.maxKnown {
		s.logf("feed rejected: known=%d new=%d max=%d; state unchanged",
			len(s.known), added, s.maxKnown)
		return nil, &InvalidInput{Reason: ErrTooManyKeys}
	}

	out := make([]Event, 0, len(events))
	for i := range events {
		key := events[i].Key
		b, ok := s.known[key]
		if !ok {
			b = bucket(key)
			s.known[key] = b
		}
		take := sampledAt(b, s.rate)
		s.logf("feed event[%d]: key=%q bucket=%d rate=%d rule=bucket<rate sampled=%t",
			i, key, b, s.rate, take)
		if take {
			out = append(out, events[i])
		}
	}
	return out, nil
}

// SetRate 调整采样率，返回（新纳入的已知键, 被移出的已知键）。
// 两个列表均按（桶值, 键）升序排序；采样率不变时两份都为空。
// 采样率非法时整体拒绝，采样率与已知键集合保持不变。
func (s *Sampler) SetRate(rate int) ([]string, []string, error) {
	if rate < 0 || rate > BucketRange {
		s.logf("setrate rejected: rate=%d out of range [0,%d]; state unchanged", rate, BucketRange)
		return nil, nil, &InvalidInput{Reason: ErrRateOutOfRange}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	newRate := uint32(rate)
	if newRate == s.rate {
		s.logf("setrate unchanged: rate=%d known=%d; both diffs empty", s.rate, len(s.known))
		return []string{}, []string{}, nil
	}

	added := make([]keyBucket, 0)
	removed := make([]keyBucket, 0)
	for key, b := range s.known {
		was := sampledAt(b, s.rate)
		now := sampledAt(b, newRate)
		switch {
		case !was && now:
			added = append(added, keyBucket{key: key, bucket: b})
		case was && !now:
			removed = append(removed, keyBucket{key: key, bucket: b})
		}
	}
	sortKeyBuckets(added)
	sortKeyBuckets(removed)

	oldRate := s.rate
	s.rate = newRate

	s.logf("setrate: %d -> %d, added=%v removed=%v",
		oldRate, newRate, keysOf(added), keysOf(removed))
	return keysOf(added), keysOf(removed), nil
}

// Rate 返回当前采样率。
func (s *Sampler) Rate() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return int(s.rate)
}

// Sampled 判断单个键在当前采样率下是否会被采出（不改已知键集合）。
func (s *Sampler) Sampled(key string) (bool, error) {
	if key == "" {
		s.logf("sampled rejected: empty key")
		return false, &InvalidInput{Reason: ErrEmptyKey}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	b := bucket(key)
	take := sampledAt(b, s.rate)
	s.logf("sampled: key=%q bucket=%d rate=%d rule=bucket<rate sampled=%t",
		key, b, s.rate, take)
	return take, nil
}

// KnownKeys 返回当前已知键数量。
func (s *Sampler) KnownKeys() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.known)
}

// SelfCheck 校验采样器内部不变量。
func (s *Sampler) SelfCheck() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var problems []string
	if s.rate > BucketRange {
		problems = append(problems, fmt.Sprintf("rate %d exceeds %d", s.rate, BucketRange))
	}
	if s.maxKnown > 0 && len(s.known) > s.maxKnown {
		problems = append(problems, fmt.Sprintf("known keys %d exceed max %d", len(s.known), s.maxKnown))
	}
	for key, stored := range s.known {
		if b := bucket(key); b != stored {
			problems = append(problems, fmt.Sprintf("stale bucket for %q: stored=%d actual=%d", key, stored, b))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("sampling: self-check failed: %s", strings.Join(problems, "; "))
	}
	s.logf("self-check ok: rate=%d known=%d maxKnownKeys=%d", s.rate, len(s.known), s.maxKnown)
	return nil
}

type keyBucket struct {
	key    string
	bucket uint32
}

func sortKeyBuckets(xs []keyBucket) {
	sort.Slice(xs, func(i, j int) bool {
		if xs[i].bucket != xs[j].bucket {
			return xs[i].bucket < xs[j].bucket
		}
		return xs[i].key < xs[j].key
	})
}

func keysOf(xs []keyBucket) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = x.key
	}
	return out
}

func (s *Sampler) logf(format string, args ...any) {
	if s.log != nil {
		s.log.Printf(format, args...)
	}
}
