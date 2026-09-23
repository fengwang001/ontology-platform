package align

import (
	"sort"
	"sync"

	"ontology/agg"
	"ontology/point"
)

// Window 是带容量上限的滑动对齐窗口。并发安全。
// 仅接受迟到跨度小于 maxBuckets 个桶的点（bounded lateness）；
// 一旦某桶被吐出，更早的点会被拒绝（ErrLate）以保证结果不重复、可复现。
type Window struct {
	mu        sync.Mutex
	step      int64
	max       int64
	buckets   map[int64]*agg.Accumulator
	lowest    int64 // 当前驻留桶中的最小网格序号
	hasLow    bool
	emitted   int64 // 已吐出水位：idx < emitted 的点拒绝
	hasEmit   bool
	processed int64
	skipped   int64
	late      int64
	peak      int
}

// NewWindow 创建窗口；step 非法或 maxBuckets <= 0 返回 ErrInvalidStep。
func NewWindow(step int64, maxBuckets int) (*Window, error) {
	if step <= 0 || maxBuckets <= 0 {
		return nil, ErrInvalidStep
	}
	return &Window{step: step, max: int64(maxBuckets),
		buckets: make(map[int64]*agg.Accumulator)}, nil
}

// Push 处理一个点。NaN 点被拒绝计入跳过数；过迟点计入 late 数。
// 返回的 out 是本次插入导致滑动吐出的已完成桶（按起点升序）。
func (w *Window) Push(p point.Point) (out []agg.Bucket, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if e := p.Valid(); e != nil {
		w.skipped++
		return nil, e
	}
	idx, _ := BucketIndex(p.TS, w.step)
	if w.hasEmit && idx < w.emitted {
		w.late++
		return nil, ErrLate
	}
	// 先按将要插入的位置滑动窗口，保证任何时刻驻留桶数不超过上限。
	if !w.hasLow {
		w.lowest = idx
		w.hasLow = true
	} else if idx < w.lowest {
		w.lowest = idx
	}
	if idx-w.lowest >= w.max {
		out = w.evict(idx - w.max + 1)
	}
	start := idx * w.step
	acc, ok := w.buckets[start]
	if !ok {
		acc = agg.New(start)
		w.buckets[start] = acc
	}
	acc.Add(p.Value)
	w.processed++
	if n := len(w.buckets); n > w.peak {
		w.peak = n
	}
	return out, nil
}

// evict 在持锁状态下吐出 idx < horizon 的桶，并推进水位。
func (w *Window) evict(horizon int64) []agg.Bucket {
	done := make([]int64, 0)
	for start := range w.buckets {
		if (start / w.step) < horizon {
			done = append(done, start)
		}
	}
	sort.Slice(done, func(i, j int) bool { return done[i] < done[j] })
	out := make([]agg.Bucket, 0, len(done))
	for _, s := range done {
		out = append(out, w.buckets[s].Result())
		delete(w.buckets, s)
	}
	w.emitted = horizon
	w.hasEmit = true
	if len(w.buckets) == 0 {
		w.hasLow = false
		return out
	}
	next := horizon + w.max - 1
	for s := range w.buckets {
		if i := s / w.step; i < next {
			next = i
		}
	}
	w.lowest = next
	return out
}

// Close 按起点升序吐出全部剩余桶。
func (w *Window) Close() []agg.Bucket {
	w.mu.Lock()
	defer w.mu.Unlock()
	starts := make([]int64, 0, len(w.buckets))
	for s := range w.buckets {
		starts = append(starts, s)
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
	out := make([]agg.Bucket, 0, len(starts))
	for _, s := range starts {
		out = append(out, w.buckets[s].Result())
	}
	w.buckets = make(map[int64]*agg.Accumulator)
	w.hasLow = false
	if len(out) > 0 {
		w.emitted = starts[len(starts)-1]/w.step + 1
		w.hasEmit = true
	}
	return out
}

// Processed 返回成功处理（进入且仅进入一次聚合）的点数。
func (w *Window) Processed() int64 { w.mu.Lock(); defer w.mu.Unlock(); return w.processed }

// Skipped 返回因 NaN 被拒绝的点数。
func (w *Window) Skipped() int64 { w.mu.Lock(); defer w.mu.Unlock(); return w.skipped }

// Late 返回因超出迟到容量被拒绝的点数。
func (w *Window) Late() int64 { w.mu.Lock(); defer w.mu.Unlock(); return w.late }

// PeakResident 返回历史上同时驻留的最大桶数。
func (w *Window) PeakResident() int { w.mu.Lock(); defer w.mu.Unlock(); return w.peak }
