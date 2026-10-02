package scheduler

// 可动态拆分、带批确认的偏移区间追踪调度器。
//
// 规则概览（详见 README）：
//   - 区间 [from,to)，next 为下一个未领取偏移；next==to 即领完。
//   - Process 领取半开批；Ack 前批压住区间 hold。
//   - hold = min(next, 最小未确认批起点)；完成区间不参与。
//   - 每次被接受的状态变更后 W = max(W, 全体未完成区间 hold 的最小值)；
//     没有未完成区间时 W 不变。
//
// 实现要点：
//   - sync.Mutex 串行化全部操作，Split 整体加锁，原子可见。
//   - 全局 hold 堆与每区间未确认批堆均惰性丢弃失效项；
//     holdProbes 仅统计 peek/pop 查看的项，规模无关。

import (
	"container/heap"
	"errors"
	"math/big"
	"sync"
)

var (
	ErrInvalid        = errors.New("scheduler: invalid argument")
	ErrBelowWatermark = errors.New("scheduler: from below watermark")
	ErrNoInterval     = errors.New("scheduler: no such interval")
	ErrExhausted      = errors.New("scheduler: interval exhausted")
	ErrNoBatch        = errors.New("scheduler: no such batch")
	ErrAlreadyAcked   = errors.New("scheduler: batch already acknowledged")
)

// ErrCannotSplit 是 Split 在 sp >= to 时返回的“正常结果”，不改任何状态。
var ErrCannotSplit = errors.New("scheduler: cannot split: split point reaches to")

const (
	MaxOffset = int64(1_000_000_000_000_000) // 1e15
	MaxN      = int64(1_000_000)
	MaxDen    = int64(1_000_000)
)

// Batch 是一次 Process 领取的偏移批。
type Batch struct {
	ID       int64 // 批号 b，全局从 1 起
	Interval int64 // 所属区间编号
	From     int64 // 批起点（领取时的 next）
	To       int64 // 批上界（领取后的 next）
	Acked    bool
}

// ProgressInfo 是 Progress 的返回快照。
type ProgressInfo struct {
	From      int64
	To        int64
	Next      int64
	Remaining int64
	Pending   int // 未确认批数
}

// Scheduler 支持并发调用；所有操作等价于某个串行顺序。
type Scheduler struct {
	mu sync.Mutex

	// idCount 同时服务 Add 与 Split，编号不复用。
	idCount int64
	// batchCount 全局批号，从 1 起。
	batchCount int64

	intervals map[int64]*interval
	batches   map[int64]*batch

	// 未完成区间的 (hold, ver) 最小堆；hold 变化后旧项靠 ver 失效。
	global globalHeap

	watermark int64

	// holdProbes：求最小 hold 时查看的堆项数（含被惰性丢弃的失效项）。
	holdProbes int64
}

// New 创建调度器，W 初值为 0。
func New() *Scheduler {
	return &Scheduler{
		intervals: make(map[int64]*interval),
		batches:   make(map[int64]*batch),
	}
}

// HoldProbes 返回计数器当前值（非导出语义的只读访问器，便于测试复现）。
func (s *Scheduler) HoldProbes() int64 {
	s.mu.Lock()
	v := s.holdProbes
	s.mu.Unlock()
	return v
}

// Watermark 返回当前输出水位线 W。
func (s *Scheduler) Watermark() int64 {
	s.mu.Lock()
	w := s.watermark
	s.mu.Unlock()
	return w
}

// advanceW 在被接受且改变状态的操作之后更新 W。
// 调用方持锁。
func (s *Scheduler) advanceW() {
	if minHold, ok := s.globalMin(); ok {
		if minHold > s.watermark {
			s.watermark = minHold
		}
	}
	// 无未完成区间：W 保持不变，不跳到无穷。
}

// Add 登记新区间 [from,to)，返回新编号。
func (s *Scheduler) Add(from, to int64) (int64, error) {
	if from < 0 || to < 0 || to > MaxOffset || from >= to {
		return 0, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if from < s.watermark {
		return 0, ErrBelowWatermark
	}

	s.idCount++
	id := s.idCount
	iv := &interval{
		id:   id,
		from: from,
		to:   to,
		next: from,
		ver:  1,
	}
	s.intervals[id] = iv
	heap.Push(&s.global, gItem{id: id, hold: from, ver: iv.ver})

	s.advanceW()
	return id, nil
}

// Process 从区间 id 领取 k=min(n,to-next) 个连续偏移。
// 返回 k、半开区间 [lo,hi) 与批号 b。
func (s *Scheduler) Process(id, n int64) (k, lo, hi, b int64, err error) {
	if n < 1 || n > MaxN || id < 1 {
		return 0, 0, 0, 0, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	iv := s.intervals[id]
	if iv == nil {
		return 0, 0, 0, 0, ErrNoInterval
	}
	if iv.next >= iv.to {
		return 0, 0, 0, 0, ErrExhausted
	}

	k = iv.to - iv.next
	if n < k {
		k = n
	}
	lo, hi = iv.next, iv.next+k
	iv.next = hi

	s.batchCount++
	b = s.batchCount
	s.batches[b] = &batch{id: b, interval: id, from: lo, to: hi}
	iv.pending++
	heap.Push(&iv.pendHeap, b)

	// hold = min(next, 最早批起点) = 最早批起点，新批不会改变它：
	// 区间此前的 hold 也是最早批起点（pending>0 时）或旧 next（== 新批起点）。
	// 因此不产生新版本，也不必重算 W；W 不回退、不前进。
	return k, lo, hi, b, nil
}

// Split 按 num/den 保留原区间剩余偏移，其余拆为新区间。
// keep = max(1, ceil(rem*num/den))，sp = next+keep。
// sp >= to 时返回 ErrCannotSplit，状态不变。
func (s *Scheduler) Split(id, num, den int64) (int64, error) {
	if num < 0 || den < 1 || den > MaxDen || num > den || id < 1 {
		return 0, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	iv := s.intervals[id]
	if iv == nil {
		return 0, ErrNoInterval
	}
	if iv.next >= iv.to {
		return 0, ErrExhausted
	}

	rem := iv.to - iv.next
	var keep int64 = 1
	if num > 0 {
		// rem*num 可达 1e15*1e6 = 1e21，超出 int64，使用大整数向上取整。
		p := new(big.Int).SetInt64(rem)
		p.Mul(p, big.NewInt(num))
		p.Add(p, big.NewInt(den-1))
		p.Quo(p, big.NewInt(den))
		if c := p.Int64(); c > keep {
			keep = c
		}
	}
	sp := iv.next + keep
	if sp >= iv.to {
		return 0, ErrCannotSplit
	}

	// 原子步骤：缩原区间 + 登记新区间在同一把锁内完成。
	oldTo := iv.to
	iv.to = sp
	// 原区间的 hold 只依赖 next 与未确认批起点，二者均不受 to 缩小影响，
	// 故其全局堆旧项依然有效。

	s.idCount++
	newID := s.idCount
	niv := &interval{
		id:   newID,
		from: sp,
		to:   oldTo,
		next: sp,
		ver:  1,
	}
	s.intervals[newID] = niv
	heap.Push(&s.global, gItem{id: newID, hold: sp, ver: niv.ver})

	s.advanceW()
	return newID, nil
}

// Ack 把批 b 置为已确认。
func (s *Scheduler) Ack(b int64) error {
	if b < 1 {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	bt := s.batches[b]
	if bt == nil {
		return ErrNoBatch
	}
	if bt.acked {
		return ErrAlreadyAcked
	}

	bt.acked = true
	iv := s.intervals[bt.interval]
	iv.pending--

	if iv.next >= iv.to && iv.pending == 0 {
		// 区间完成：旧堆项全部失效。
		iv.done = true
		iv.ver++
	} else {
		// 只有当被确认的是当前最早未确认批（或堆顶残留链）时，
		// 最小未确认批起点才可能变化。先看堆顶确认这一点。
		s.holdProbes++ // 查看堆顶（无论是否即将被丢弃）
		topID := iv.pendHeap[0]
		if topID == b {
			heap.Pop(&iv.pendHeap)
			newHold := iv.next
			if iv.pending > 0 {
				newHold = s.minPendingFrom(iv)
			}
			iv.ver++
			heap.Push(&s.global, gItem{id: iv.id, hold: newHold, ver: iv.ver})
		}
		// 堆顶是别的未确认批时：hold 不变（乱序 Ack 后面的批）。
	}

	s.advanceW()
	return nil
}

// Progress 返回 from、to、next、remaining 与未确认批数。
func (s *Scheduler) Progress(id int64) (ProgressInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	iv := s.intervals[id]
	if iv == nil {
		return ProgressInfo{}, ErrNoInterval
	}
	return ProgressInfo{
		From:      iv.from,
		To:        iv.to,
		Next:      iv.next,
		Remaining: iv.to - iv.next,
		Pending:   iv.pending,
	}, nil
}
