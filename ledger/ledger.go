package ledger

import (
	"container/heap"
	"sort"
	"sync"
)

// ErrReason 标识一次被整体拒绝的操作的可区分原因。
type ErrReason int

const (
	// ErrTagIllegal：标签为 0 或大于已分配的最大标签。
	ErrTagIllegal ErrReason = iota
	// ErrAlreadySettled：非批量时该标签已结算（已确认、已丢弃或已回队）。
	ErrAlreadySettled
	// ErrRangeEmpty：批量时标签合法但 [1, t] 内没有任何未确认投递。
	ErrRangeEmpty
	// ErrPrefetchFull：投递时未确认数已达预取上限（优先于队列为空）。
	ErrPrefetchFull
	// ErrQueueEmpty：投递时未确认数未满但队列中没有消息。
	ErrQueueEmpty
)

func (r ErrReason) String() string {
	switch r {
	case ErrTagIllegal:
		return "tag illegal: tag is 0 or greater than the largest tag ever allocated"
	case ErrAlreadySettled:
		return "tag already settled: acknowledged, discarded, or requeued"
	case ErrRangeEmpty:
		return "batch range empty: no unacked delivery with tag <= t"
	case ErrPrefetchFull:
		return "prefetch full: unacked count already equals prefetch limit P"
	case ErrQueueEmpty:
		return "queue empty: no message available for delivery"
	default:
		return "unknown ledger error"
	}
}

// Error 是所有拒绝原因的错误类型，可用 errors.As 区分原因。
type Error struct{ Reason ErrReason }

func (e *Error) Error() string { return e.Reason.String() }

// Delivery 是一次投递的结果。
type Delivery[T any] struct {
	// Tag 是本次投递分配的投递标签（每次投递都分配新标签，含重投）。
	Tag uint64
	// Seq 是消息的入队序号（只在入队时分配一次，回队不变）。
	Seq uint64
	// Message 是入队的消息。
	Message T
	// Redeliver 为假表示首次投递，为真表示该消息曾回队后再次投递。
	Redeliver bool
}

type phase uint8

const (
	phaseQueued phase = iota
	phaseUnacked
	phaseSettled
)

type entry[T any] struct {
	seq         uint64
	message     T
	phase       phase
	redelivered bool
}

// Ledger 是带预取上限的通道未确认投递账本。
// 所有方法均可并发调用，可线性化（等价于某个串行顺序）。
type Ledger[T any] struct {
	mu       sync.Mutex
	prefetch int

	nextSeq uint64 // 下一个入队序号
	maxTag  uint64 // 已分配的最大投递标签

	entries map[uint64]*entry[T] // 入队序号 -> 消息条目（含已结算）
	queue   *seqHeap             // entries 中 phase == phaseQueued 的序号最小堆
	unacked map[uint64]uint64    // 投递标签 -> 入队序号（phase == phaseUnacked）

	dropped int // 拒绝且不回队而丢弃的消息数
}

// New 创建预取上限为 P 的账本（P 必须 >= 1）。
func New[T any](prefetch int) *Ledger[T] {
	if prefetch < 1 {
		panic("ledger: prefetch must be >= 1")
	}
	return &Ledger[T]{
		prefetch: prefetch,
		entries:  make(map[uint64]*entry[T]),
		queue:    &seqHeap{},
		unacked:  make(map[uint64]uint64),
	}
}

// Enqueue 消息入队：分配入队序号（从 1 起，只增不复用）并放入队列。
// 队列始终按入队序号持有消息。
func (l *Ledger[T]) Enqueue(msg T) uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.nextSeq++
	seq := l.nextSeq
	l.entries[seq] = &entry[T]{seq: seq, message: msg, phase: phaseQueued}
	heap.Push(l.queue, seq)
	return seq
}

// Deliver 投递：仅当未确认数小于 P 时，从队列取入队序号最小者，
// 分配新的投递标签（每次投递加一，重投也分配新标签）。
// 首次投递 Redeliver 为假；回队后再次投递为真。
// 预取已满优先于队列为空报错。
func (l *Ledger[T]) Deliver() (Delivery[T], error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.unacked) >= l.prefetch {
		return Delivery[T]{}, &Error{Reason: ErrPrefetchFull}
	}
	if l.queue.Len() == 0 {
		return Delivery[T]{}, &Error{Reason: ErrQueueEmpty}
	}

	seq := heap.Pop(l.queue).(uint64)
	e := l.entries[seq]
	e.phase = phaseUnacked

	l.maxTag++
	tag := l.maxTag
	l.unacked[tag] = seq

	d := Delivery[T]{
		Tag:       tag,
		Seq:       seq,
		Message:   e.message,
		Redeliver: e.redelivered,
	}
	// 本次投递之后再回队重投，标志即为真。
	e.redelivered = true
	return d, nil
}

// Ack 确认标签 t：multiple 为假只确认 t 对应投递；为真确认当前所有
// 标签不大于 t 的未确认投递。被确认者永久移除（结算）。
// 校验先于任何状态变更，被拒绝时账本保持不变。
func (l *Ledger[T]) Ack(tag uint64, multiple bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	tags, err := l.collectTags(tag, multiple)
	if err != nil {
		return err
	}
	for _, tg := range tags {
		seq := l.unacked[tg]
		delete(l.unacked, tg)
		l.entries[seq].phase = phaseSettled
	}
	return nil
}

// Nack 拒绝标签 t：范围取法同 Ack。requeue 为真时范围内消息按入队序号
// 升序归位回队（堆结构天然保证按序号取最小者）；为假时丢弃并累加丢弃数。
// 校验先于任何状态变更，被拒绝时账本保持不变。
func (l *Ledger[T]) Nack(tag uint64, multiple bool, requeue bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	tags, err := l.collectTags(tag, multiple)
	if err != nil {
		return err
	}
	for _, tg := range tags {
		seq := l.unacked[tg]
		delete(l.unacked, tg)
		e := l.entries[seq]
		if requeue {
			e.phase = phaseQueued
			heap.Push(l.queue, seq)
		} else {
			e.phase = phaseSettled
			l.dropped++
		}
	}
	return nil
}

// collectTags 在持锁状态下计算本次操作影响的标签集合（按标签升序），
// 并执行“整体拒绝”校验；返回错误时不产生任何变更。
func (l *Ledger[T]) collectTags(tag uint64, multiple bool) ([]uint64, error) {
	if tag == 0 || tag > l.maxTag {
		return nil, &Error{Reason: ErrTagIllegal}
	}
	if !multiple {
		if _, ok := l.unacked[tag]; !ok {
			return nil, &Error{Reason: ErrAlreadySettled}
		}
		return []uint64{tag}, nil
	}
	var tags []uint64
	for tg := range l.unacked {
		if tg <= tag {
			tags = append(tags, tg)
		}
	}
	if len(tags) == 0 {
		return nil, &Error{Reason: ErrRangeEmpty}
	}
	sort.Slice(tags, func(i, j int) bool { return tags[i] < tags[j] })
	return tags, nil
}

// Prefetch 返回预取上限 P。
func (l *Ledger[T]) Prefetch() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.prefetch
}

// UnackedCount 返回当前未确认投递数（始终 <= P）。
func (l *Ledger[T]) UnackedCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.unacked)
}

// QueueLen 返回队列中等待投递的消息数。
func (l *Ledger[T]) QueueLen() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.queue.Len()
}

// MaxTag 返回已分配的最大投递标签（尚未投递过为 0）。
func (l *Ledger[T]) MaxTag() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.maxTag
}

// Dropped 返回因拒绝且不回队而丢弃的消息数。
func (l *Ledger[T]) Dropped() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.dropped
}

// Unacked 返回当前未确认投递的快照（按标签升序）。
func (l *Ledger[T]) Unacked() []Delivery[T] {
	l.mu.Lock()
	defer l.mu.Unlock()

	tags := make([]uint64, 0, len(l.unacked))
	for tg := range l.unacked {
		tags = append(tags, tg)
	}
	sort.Slice(tags, func(i, j int) bool { return tags[i] < tags[j] })

	out := make([]Delivery[T], 0, len(tags))
	for _, tg := range tags {
		seq := l.unacked[tg]
		e := l.entries[seq]
		out = append(out, Delivery[T]{
			Tag:       tg,
			Seq:       seq,
			Message:   e.message,
			Redeliver: e.redelivered,
		})
	}
	return out
}

// QueuedMessages 返回队列消息快照，按入队序号升序。
func (l *Ledger[T]) QueuedMessages() []T {
	l.mu.Lock()
	defer l.mu.Unlock()

	seqs := append([]uint64(nil), (*l.queue)...)
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	out := make([]T, 0, len(seqs))
	for _, seq := range seqs {
		out = append(out, l.entries[seq].message)
	}
	return out
}

// seqHeap 是入队序号的最小堆，实现 heap.Interface。
type seqHeap []uint64

func (h seqHeap) Len() int           { return len(h) }
func (h seqHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h seqHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *seqHeap) Push(x any) { *h = append(*h, x.(uint64)) }

func (h *seqHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}
