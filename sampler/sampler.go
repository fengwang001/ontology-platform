package sampler

import (
	"container/heap"
	"fmt"
	"math"
	"sort"
	"sync"
)

// Sampler 是并发安全的加权不放回流式抽样器。
//
// 算法（Efraimidis–Spirakis 加权蓄水池抽样）：每个被接受的元素（即通过全部
// 输入校验、提交成功的元素）从注入随机源消耗一个随机数 u∈(0,1)，得到键值
// key = u^(1/weight)。样本始终保留键值最大的 sampleSize 个元素；键值相等时
// 先到达者排名更高。新元素仅在严格高于当前样本中排名最低者时才将其替换。
// 该流式算法与“对全部元素统一算键值后排序取前 sampleSize 个”的一次性批量
// 计算结果完全一致。
//
// 术语：提交被“拒绝”指因输入非法（空/重复标识、零权重、随机数问题等）返回
// 错误，被拒提交不改变样本，也不推进随机源游标；提交成功但未留在最终样本中
// 的元素称为“出局”，它所消耗的随机数与批量算法一样照常计入消耗。
type Sampler struct {
	mu       sync.Mutex
	size     int
	source   Source
	logger   Logger
	seen     map[string]struct{}
	entries  sampledHeap
	order    int
	consumed int
}

// Option 配置抽样器。
type Option func(*Sampler)

// WithLogger 注入步骤日志记录器。
func WithLogger(logger Logger) Option {
	return func(s *Sampler) {
		if logger != nil {
			s.logger = logger
		}
	}
}

// New 创建一个保留键值最大的 sampleSize 个元素的抽样器。
func New(sampleSize int, source Source, opts ...Option) (*Sampler, error) {
	if sampleSize <= 0 {
		return nil, ErrInvalidSampleSize
	}
	if source == nil {
		return nil, fmt.Errorf("%w: nil random source", ErrIllegalRandom)
	}
	s := &Sampler{
		size:   sampleSize,
		source: source,
		logger: nopLogger{},
		seen:   make(map[string]struct{}),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// Submit 提交一个带权元素；accepted 表示元素是否进入当前样本集合。
// 任何输入错误都会整体拒绝：样本与随机源游标保持调用前状态。
func (s *Sampler) Submit(id string, weight float64) (accepted bool, err error) {
	if id == "" {
		s.logf("reject: empty id (weight=%v): %v", weight, ErrEmptyID)
		return false, ErrEmptyID
	}
	if math.IsNaN(weight) || math.IsInf(weight, 0) || weight < 0 {
		s.logf("reject: id=%q illegal weight=%v: %v", id, weight, ErrIllegalWeight)
		return false, ErrIllegalWeight
	}
	if weight == 0 {
		s.logf("reject: id=%q zero weight: %v", id, ErrZeroWeight)
		return false, ErrZeroWeight
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, dup := s.seen[id]; dup {
		s.logf("reject: duplicate id=%q: %v", id, ErrDuplicateID)
		return false, ErrDuplicateID
	}

	// 取随机数失败（用尽或数值非法）时不得改变任何状态：
	// 此时尚未写入 seen / entries / 计数器。
	u, drawErr := s.source.Next()
	if drawErr != nil {
		s.logf("reject: id=%q draw failed: %v", id, drawErr)
		return false, drawErr
	}

	order := s.order
	s.order++
	s.consumed++
	s.seen[id] = struct{}{}

	candidate := Selected{
		Element: Element{ID: id, Weight: weight},
		Key:     keyOf(weight, u),
		Order:   order,
	}

	if s.entries.Len() < s.size {
		heap.Push(&s.entries, candidate)
		s.logf("step order=%d id=%q weight=%v u=%.6f key=%.9f -> ACCEPT (fill %d/%d)",
			order, id, weight, u, candidate.Key, s.entries.Len(), s.size)
		return true, nil
	}

	worst := s.entries[0]
	if ranksHigher(candidate, worst) {
		s.logf("step order=%d id=%q weight=%v u=%.6f key=%.9f -> REPLACE id=%q key=%.9f (strictly higher than lowest)",
			order, id, weight, u, candidate.Key, worst.Element.ID, worst.Key)
		s.entries[0] = candidate
		heap.Fix(&s.entries, 0)
		return true, nil
	}

	reason := "key lower than lowest"
	if candidate.Key == worst.Key {
		reason = "key tied with lowest but arrived later"
	}
	s.logf("step order=%d id=%q weight=%v u=%.6f key=%.9f -> OUT (lowest id=%q key=%.9f; %s)",
		order, id, weight, u, candidate.Key, worst.Element.ID, worst.Key, reason)
	return false, nil
}

// Samples 返回当前样本，按键值降序排列；键值并列时先到达者在前。
func (s *Sampler) Samples() []Selected {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Selected, len(s.entries))
	copy(out, s.entries)
	sort.Slice(out, func(i, j int) bool { return ranksHigher(out[i], out[j]) })
	return out
}

// Consumed 返回已消耗的随机数个数。
func (s *Sampler) Consumed() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.consumed
}

// Size 返回抽样器配置的样本个数。
func (s *Sampler) Size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.size
}

// Check 执行内部不变量自检。
func (s *Sampler) Check() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.entries.Len() > s.size {
		return fmt.Errorf("sampler self-check: sample set %d exceeds size %d", s.entries.Len(), s.size)
	}
	if s.consumed != len(s.seen) {
		return fmt.Errorf("sampler self-check: consumed=%d but accepted elements=%d", s.consumed, len(s.seen))
	}
	// 堆顶必须是排名最低者（键值最小；并列时到达最晚者）。
	for i := 1; i < s.entries.Len(); i++ {
		if ranksHigher(s.entries[i], s.entries[0]) {
			continue
		}
		if s.entries[i].Key != s.entries[0].Key || s.entries[i].Order == s.entries[0].Order {
			return fmt.Errorf("sampler self-check: heap root is not the lowest-ranked entry")
		}
	}
	// 抽样器视角的消耗数应与底层随机源游标一致（当随机源支持观测时）。
	if tracked, ok := s.source.(interface{ Consumed() int }); ok {
		if got := tracked.Consumed(); got != s.consumed {
			return fmt.Errorf("sampler self-check: source consumed=%d but sampler consumed=%d", got, s.consumed)
		}
	}
	return nil
}

// keyOf 计算键值 r^(1/weight)。
func keyOf(weight, r float64) float64 {
	return math.Pow(r, 1.0/weight)
}

// ranksHigher 报告 a 的排名是否严格高于 b：键值更大；键值相等时先到达者更高。
func ranksHigher(a, b Selected) bool {
	if a.Key != b.Key {
		return a.Key > b.Key
	}
	return a.Order < b.Order
}

func (s *Sampler) logf(format string, args ...any) {
	s.logger.Logf(format, args...)
}

type nopLogger struct{}

func (nopLogger) Logf(string, ...any) {}

// sampledHeap 是小顶堆，堆顶始终为当前样本中排名最低的元素。
type sampledHeap []Selected

func (h sampledHeap) Len() int { return len(h) }

func (h sampledHeap) Less(i, j int) bool {
	// i 排在 j “更靠堆顶”当且仅当 i 的排名更低。
	return ranksHigher(h[j], h[i])
}

func (h sampledHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *sampledHeap) Push(x any) { *h = append(*h, x.(Selected)) }

func (h *sampledHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}
