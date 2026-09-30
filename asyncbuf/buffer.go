// Package asyncbuf 实现异步输入输出算子的有序（Ordered）与无序
// （Unordered）输出缓冲。
//
// 缓冲接收元素与水位线交织的输入流；元素的异步请求何时完成由调用方通过
// Complete 声明。缓冲根据所处模式决定哪些条目可以输出：

//   - Ordered：严格按输入顺序输出，仅当队头是已完成元素或水位线时输出。
//   - Unordered：相邻水位线把元素划分为若干段；只有当前已开放段中的元素
//     完成后立即输出，未开放段中完成的元素被扣住；水位线在其之前全部元素
//     输出后立即输出，并级联开放后续段、按完成先后释放被扣元素。
//
// 所有方法都可被多个 goroutine 并发调用。任何被拒绝的操作在判定非法后立即
// 返回，不会修改队列、占用数或已产生的输出（失败不留痕）。
package asyncbuf

import (
	"fmt"
	"sync"
)

// Mode 为输出顺序模式。
type Mode int

const (
	// Ordered 严格保序输出。
	Ordered Mode = iota
	// Unordered 按水位线分段、段内按完成先后输出。
	Unordered
)

func (m Mode) String() string {
	switch m {
	case Ordered:
		return "Ordered"
	case Unordered:
		return "Unordered"
	default:
		return "Unknown"
	}
}

// OutputKind 区分输出条目是元素还是水位线。
type OutputKind int

const (
	// ElementOutput 输出的是一个已完成元素。
	ElementOutput OutputKind = iota + 1
	// WatermarkOutput 输出的是一条水位线。
	WatermarkOutput
)

// Output 为一条已经判定可以输出的条目。
type Output struct {
	Kind      OutputKind
	ID        string // Kind==ElementOutput 时有效
	Value     any    // Kind==ElementOutput 时有效
	Watermark int64  // Kind==WatermarkOutput 时有效

	// 元信息，便于日志与测试核对，不影响判定。
	Index int // 该条目在输入流中的序号（从 0 起）
}

// entry 为内部队列条目：要么是元素，要么是水位线。
type entry struct {
	isWatermark bool

	// 元素字段
	id        string
	value     any
	completed bool
	doneSeq   int64 // 完成声明的全局先后序号；越小越早完成

	// 水位线字段
	watermark int64

	// 输入流序号
	index int
}

// Buffer 是并发安全的异步输出缓冲。零值不可用，请用 New 构造。
type Buffer struct {
	mu sync.Mutex

	mode     Mode
	capacity int

	// queue 保存所有尚未输出的条目，保持输入顺序。
	queue []*entry
	// ids 按标识索引仍在 queue 中的元素（无论是否已完成）。
	ids map[string]*entry

	// occupied 为当前仍占用容量的元素数（queue 中的元素条目数）。
	occupied int

	// outputs 为已判定可输出、等待 Drain 取走的结果。
	outputs []Output

	lastWM int64
	hasWM  bool

	nextIndex int
	nextSeq   int64
}

// New 创建缓冲。capacity 必须 >=1，mode 必须是 Ordered 或 Unordered。
func New(mode Mode, capacity int) (*Buffer, error) {
	if mode != Ordered && mode != Unordered {
		return nil, &Error{Kind: ErrInvalidArgument, Detail: "unknown mode"}
	}
	if capacity < 1 {
		return nil, &Error{Kind: ErrInvalidArgument, Detail: "capacity must be >= 1"}
	}
	return &Buffer{
		mode:     mode,
		capacity: capacity,
		queue:    make([]*entry, 0),
		ids:      make(map[string]*entry),
	}, nil
}

// Mode 返回输出模式。
func (b *Buffer) Mode() Mode {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.mode
}

// Capacity 返回容量上限。
func (b *Buffer) Capacity() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.capacity
}

// Occupied 返回当前占用容量的元素数（已提交但尚未输出）。
func (b *Buffer) Occupied() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.occupied
}

// Pending 返回队列中尚未输出的条目数（元素 + 水位线）。
func (b *Buffer) Pending() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.queue)
}

// Buffered 返回已判定可输出、但尚未被 Drain 取走的条目数。
func (b *Buffer) Buffered() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.outputs)
}

// Submit 提交一个元素。若空标识、重复标识或容量已满，则整体拒绝且不留痕。
// 元素提交后处于未完成状态，直到调用方调用 Complete 声明其异步请求完成。
func (b *Buffer) Submit(id string, value any) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	// 全部判定先于任何状态变更，保证失败不留痕。
	if id == "" {
		return &Error{Kind: ErrEmptyID}
	}
	if _, exists := b.ids[id]; exists {
		return &Error{Kind: ErrDuplicateID, ID: id}
	}
	if b.occupied >= b.capacity {
		return &Error{
			Kind:   ErrCapacityFull,
			ID:     id,
			Detail: detailCapacity(b.occupied, b.capacity),
		}
	}

	e := &entry{
		id:    id,
		value: value,
		index: b.nextIndex,
	}
	b.nextIndex++
	b.queue = append(b.queue, e)
	b.ids[id] = e
	b.occupied++

	b.advanceLocked()
	return nil
}

// SubmitWatermark 提交一条水位线。水位线必须相对上一条严格递增，否则拒绝。
func (b *Buffer) SubmitWatermark(wm int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.hasWM && wm <= b.lastWM {
		return &Error{
			Kind:      ErrWatermarkNotMonotonic,
			Watermark: wm,
			Detail:    detailWatermark(b.lastWM),
		}
	}

	e := &entry{
		isWatermark: true,
		watermark:   wm,
		index:       b.nextIndex,
	}
	b.nextIndex++
	b.queue = append(b.queue, e)
	b.lastWM = wm
	b.hasWM = true

	b.advanceLocked()
	return nil
}

// Complete 声明某元素的异步请求已经完成。标识未知或已完成时拒绝且不留痕。
func (b *Buffer) Complete(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	e, exists := b.ids[id]
	if !exists {
		return &Error{Kind: ErrUnknownID, ID: id}
	}
	if e.completed {
		return &Error{Kind: ErrAlreadyCompleted, ID: id}
	}

	e.completed = true
	e.doneSeq = b.nextSeq
	b.nextSeq++

	b.advanceLocked()
	return nil
}

// Drain 取走并清空当前所有可输出条目，按输出顺序返回。无输出时返回 nil。
func (b *Buffer) Drain() []Output {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.outputs) == 0 {
		return nil
	}
	out := b.outputs
	b.outputs = nil
	return out
}

// advanceLocked 在每次状态变更后，把当前能够输出的条目尽可能推入 outputs。
func (b *Buffer) advanceLocked() {
	if b.mode == Ordered {
		b.advanceOrderedLocked()
		return
	}
	b.advanceUnorderedLocked()
}

// advanceOrderedLocked：从队头起，只要是水位线或已完成元素就输出，
// 遇到未完成元素即停（队头阻塞，严格保序）。
func (b *Buffer) advanceOrderedLocked() {
	for len(b.queue) > 0 {
		head := b.queue[0]
		if head.isWatermark {
			b.emitWatermarkLocked(head)
			continue
		}
		if head.completed {
			b.emitElementLocked(head)
			continue
		}
		return
	}
}

// advanceUnorderedLocked：当前开放段是“第一条未输出水位线之前”的元素。
//
//  1. 把开放段内所有已完成元素按完成先后（doneSeq）输出；
//  2. 若队头此时是水位线，说明其之前元素已全部输出，输出该水位线并
//     回到第 1 步（级联开放下一段、释放其中被扣元素）。
func (b *Buffer) advanceUnorderedLocked() {
	for {
		b.emitOpenSegmentCompletedLocked()
		if len(b.queue) > 0 && b.queue[0].isWatermark {
			b.emitWatermarkLocked(b.queue[0])
			continue
		}
		return
	}
}

// emitOpenSegmentCompletedLocked 输出第一条水位线之前所有已完成元素，
// 按完成先后（doneSeq）升序。
func (b *Buffer) emitOpenSegmentCompletedLocked() {
	// 反复选出开放段内 doneSeq 最小的已完成元素输出，等价于按完成
	// 先后升序输出；每次输出后重扫，避免引用已删除的位置。
	for {
		var pick *entry
		for _, e := range b.queue {
			if e.isWatermark {
				break // 到达段边界：其后是未开放段
			}
			if e.completed && (pick == nil || e.doneSeq < pick.doneSeq) {
				pick = e
			}
		}
		if pick == nil {
			return
		}
		b.emitElementLocked(pick)
	}
}

func (b *Buffer) emitElementLocked(e *entry) {
	b.removeFromQueueLocked(e)
	delete(b.ids, e.id)
	b.occupied--
	b.outputs = append(b.outputs, Output{
		Kind:  ElementOutput,
		ID:    e.id,
		Value: e.value,
		Index: e.index,
	})
}

func (b *Buffer) emitWatermarkLocked(e *entry) {
	b.removeFromQueueLocked(e)
	b.outputs = append(b.outputs, Output{
		Kind:      WatermarkOutput,
		Watermark: e.watermark,
		Index:     e.index,
	})
}

// removeFromQueueLocked 按指针从 queue 中删除条目。
func (b *Buffer) removeFromQueueLocked(target *entry) {
	for i, e := range b.queue {
		if e == target {
			b.queue = append(b.queue[:i], b.queue[i+1:]...)
			return
		}
	}
}

func detailCapacity(occupied, capacity int) string {
	return fmt.Sprintf("occupied=%d capacity=%d", occupied, capacity)
}

func detailWatermark(last int64) string {
	return fmt.Sprintf("last watermark=%d", last)
}
