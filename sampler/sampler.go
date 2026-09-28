package sampler

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
)

// Logger 记录逐步判定过程，便于审计与复现。
type Logger interface {
	Printf(format string, args ...any)
}

// Element 是一次提交的输入。
type Element struct {
	ID     string
	Weight float64
}

// Sample 是输出的一条样本。
type Sample struct {
	ID     string
	Weight float64
	Key    float64
	Order  int
}

// Sampler 是并发安全的加权不放回抽样器（A-Res）。
type Sampler struct {
	mu      sync.Mutex
	k       int
	source  RandomSource
	logger  Logger
	heap    minHeap
	ids     map[string]struct{}
	arrived int // 已处理（进入判定）的到达计数，决定并列先后
	offered int // 成功提交（未整体拒绝）的元素总数
}

// New 构造容量为 k 的抽样器；非法参数整体拒绝。
func New(k int, source RandomSource, logger Logger) (*Sampler, error) {
	if k <= 0 {
		return nil, ErrInvalidSampleSize
	}
	if source == nil {
		return nil, ErrInvalidRandomSource
	}
	return &Sampler{
		k:      k,
		source: source,
		logger: logger,
		ids:    make(map[string]struct{}),
	}, nil
}

// Offer 提交一个元素；任何拒绝都不改变样本与随机源游标。
func (s *Sampler) Offer(id string, weight float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	step := s.offered + 1

	// 输入校验：在触碰随机源之前完成，失败不留痕（样本、游标、提交计数均不变）。
	if id == "" {
		s.logf("step=%d offer id=%q weight=%v -> reject: %v", step, id, weight, ErrEmptyID)
		return ErrEmptyID
	}
	switch {
	case math.IsNaN(weight) || math.IsInf(weight, 0):
		s.logf("step=%d offer id=%q weight=%v -> reject: %v", step, id, weight, ErrInvalidWeight)
		return ErrInvalidWeight
	case weight < 0:
		s.logf("step=%d offer id=%q weight=%v -> reject: %v", step, id, weight, ErrInvalidWeight)
		return ErrInvalidWeight
	case weight == 0:
		s.logf("step=%d offer id=%q weight=%v -> reject: %v", step, id, weight, ErrZeroWeight)
		return ErrZeroWeight
	}
	if _, dup := s.ids[id]; dup {
		s.logf("step=%d offer id=%q weight=%v -> reject: %v", step, id, weight, ErrDuplicateID)
		return fmt.Errorf("%w: %q", ErrDuplicateID, id)
	}

	// 取一个随机数；源用尽或返回非法值时整体拒绝，样本与游标均不变。
	u, err := s.source.Next()
	if err != nil {
		if errors.Is(err, ErrRandomExhausted) {
			s.logf("step=%d offer id=%q weight=%v -> reject: %v", step, id, weight, ErrRandomExhausted)
		} else {
			s.logf("step=%d offer id=%q weight=%v -> reject: source error %v", step, id, weight, err)
		}
		return err
	}
	if !validRandom(u) {
		s.logf("step=%d offer id=%q weight=%v u=%v -> reject: %v", step, id, weight, u, ErrInvalidRandom)
		return ErrInvalidRandom
	}

	s.offered++
	step = s.offered
	key := math.Pow(u, 1/weight)
	candidate := &entry{id: id, weight: weight, key: key, order: s.arrived}

	if s.heap.Len() < s.k {
		s.heap.push(candidate)
		s.ids[id] = struct{}{}
		s.arrived++
		s.logf("step=%d accept id=%q weight=%v u=%.12f key=%.12f -> insert (size=%d)",
			step, id, weight, u, key, s.heap.Len())
		return nil
	}

	lowest := s.heap[0]
	if key > lowest.key {
		evicted := s.heap.pop()
		delete(s.ids, evicted.id)
		s.heap.push(candidate)
		s.ids[id] = struct{}{}
		s.arrived++
		s.logf("step=%d accept id=%q weight=%v u=%.12f key=%.12f -> replace lowest id=%q key=%.12f",
			step, id, weight, u, key, evicted.id, evicted.key)
		return nil
	}

	// 排名不高于最低者（含键值相等：先到达者优先，后来者不替换）：
	// 元素被拒绝，样本不变；随机数已被该接受（参与判定）元素消耗。
	s.arrived++
	s.logf("step=%d reject id=%q weight=%v u=%.12f key=%.12f -> not above lowest id=%q key=%.12f (ties favor earlier)",
		step, id, weight, u, key, lowest.id, lowest.key)
	return nil
}

// Samples 返回按键值降序（并列时先到达者优先）排列的样本快照。
func (s *Sampler) Samples() []Sample {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries := make([]*entry, len(s.heap))
	copy(entries, s.heap)
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].key != entries[j].key {
			return entries[i].key > entries[j].key
		}
		return entries[i].order < entries[j].order
	})
	out := make([]Sample, len(entries))
	for i, e := range entries {
		out[i] = Sample{ID: e.id, Weight: e.weight, Key: e.key, Order: e.order}
	}
	return out
}

// Consumed 返回已消耗随机数个数。
func (s *Sampler) Consumed() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.source.(interface{ Consumed() int }); ok {
		return c.Consumed()
	}
	return s.arrived
}

// SelfCheck 校验内部不变量；发现破坏返回错误。
func (s *Sampler) SelfCheck() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.heap.Len() > s.k {
		return fmt.Errorf("sampler self-check: sample size %d exceeds capacity %d", s.heap.Len(), s.k)
	}
	if len(s.ids) != s.heap.Len() {
		return fmt.Errorf("sampler self-check: id set %d != heap size %d", len(s.ids), s.heap.Len())
	}
	for _, e := range s.heap {
		if _, ok := s.ids[e.id]; !ok {
			return fmt.Errorf("sampler self-check: heap id %q missing from id set", e.id)
		}
		if !validWeight(e.weight) {
			return fmt.Errorf("sampler self-check: stored weight %v invalid", e.weight)
		}
		if math.IsNaN(e.key) || math.IsInf(e.key, 0) {
			return fmt.Errorf("sampler self-check: stored key %v invalid", e.key)
		}
	}
	// 堆性质：子节点不得弱于父节点，堆顶为全局最低。
	for i := 1; i < s.heap.Len(); i++ {
		parent := (i - 1) / 2
		if s.heap.lessAt(i, parent) {
			return fmt.Errorf("sampler self-check: heap property broken at %d", i)
		}
	}
	return nil
}

func validWeight(w float64) bool {
	return !math.IsNaN(w) && !math.IsInf(w, 0) && w > 0
}

func (s *Sampler) logf(format string, args ...any) {
	if s.logger != nil {
		s.logger.Printf(format, args...)
	}
}
