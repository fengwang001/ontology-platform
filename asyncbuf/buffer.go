// Package asyncbuf 实现异步输入输出算子的有序与无序输出缓冲。
//
// 缓冲接收元素与水位线交织的输入流。元素由调用方异步声明完成，
// 缓冲根据模式（有序 / 无序）决定元素与水位线的输出顺序。
package asyncbuf

import (
	"errors"
	"io"
	"sync"
)

// Mode 决定缓冲的输出排序规则。
type Mode int

const (
	// Ordered 为有序模式：输出顺序严格等于输入顺序，
	// 队头是已完成元素或水位线时才可输出。
	Ordered Mode = iota
	// Unordered 为无序模式：水位线把输入流划分为段，
	// 仅已开放段内完成的元素按完成先后输出。
	Unordered
)

// 互不相同、可区分的拒绝原因。
var (
	// ErrCapacityFull 表示缓冲容量已被在途元素占满。
	ErrCapacityFull = errors.New("asyncbuf: capacity full")
	// ErrEmptyID 表示元素标识为空字符串。
	ErrEmptyID = errors.New("asyncbuf: empty element id")
	// ErrDuplicateID 表示元素标识与某个在途元素重复。
	ErrDuplicateID = errors.New("asyncbuf: duplicate element id")
	// ErrUnknownID 表示完成声明引用了缓冲中不存在的标识。
	ErrUnknownID = errors.New("asyncbuf: unknown element id")
	// ErrAlreadyCompleted 表示完成声明引用了已经声明完成的元素。
	ErrAlreadyCompleted = errors.New("asyncbuf: element already completed")
	// ErrWatermarkNotIncreasing 表示水位线没有严格大于上一条水位线。
	ErrWatermarkNotIncreasing = errors.New("asyncbuf: watermark not strictly increasing")
)

// Buffer 是并发安全的异步输出缓冲。
type Buffer struct {
	mu sync.Mutex

	mode Mode
	cap  int

	// entries 为所有已接收但尚未输出的事件（元素/水位线），按输入顺序排列。
	entries []*entry
	// live 为仍占用容量的元素（已接收但尚未输出），按标识索引。
	live map[string]*entry

	// orderSeq 为输入事件序号；doneSeq 为完成声明的先后序号。
	orderSeq int
	doneSeq  int

	// 水位线严格递增校验。
	hasLastWM bool
	lastWM    int64

	// 仅无序模式使用：segPending[s] 为段 s 内尚未输出的元素数。
	// 段由相邻水位线划分，第 s 条水位线是段 s 的右屏障；
	// openSeg 为当前已开放段，held 为未开放段内已完成、被扣住的元素，
	// 按完成先后（doneOrder）排列。
	segPending []int
	openSeg    int
	held       []*entry

	// outQ 为已判定可输出、等待 Output 查询取走的事件。
	outQ []Event

	log io.Writer
}

// New 创建容量为 capacity 的缓冲。capacity 必须为正数。
func New(mode Mode, capacity int) *Buffer {
	if capacity <= 0 {
		panic("asyncbuf: capacity must be positive")
	}
	return &Buffer{mode: mode, cap: capacity, live: make(map[string]*entry)}
}

// SetLogger 设置每步判定日志的输出目标，nil 表示关闭日志。
func (b *Buffer) SetLogger(w io.Writer) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.log = w
}

// PushElement 接收一个元素；容量满或标识非法时整体拒绝且不留痕。
func (b *Buffer) PushElement(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	// 校验顺序固定：空标识 → 重复标识 → 容量满。任一不过即整体拒绝，
	// 在校验通过前不触碰任何状态。
	if id == "" {
		b.logf("reject element id=\"\" reason=%q", "", errText(ErrEmptyID))
		return ErrEmptyID
	}
	if _, dup := b.live[id]; dup {
		b.logf("reject element id=%s reason=%q", id, errText(ErrDuplicateID))
		return ErrDuplicateID
	}
	if len(b.live) >= b.cap {
		b.logf("reject element id=%s reason=%q inflight=%d cap=%d",
			id, errText(ErrCapacityFull), len(b.live), b.cap)
		return ErrCapacityFull
	}

	e := &entry{kind: ElementEvent, id: id, order: b.orderSeq, seg: b.openSeg}
	b.orderSeq++
	b.entries = append(b.entries, e)
	b.live[id] = e
	if b.mode == Unordered {
		for len(b.segPending) <= e.seg {
			b.segPending = append(b.segPending, 0)
		}
		b.segPending[e.seg]++
	}
	b.logf("push element id=%s order=%d accepted inflight=%d", id, e.order, len(b.live))

	b.pump()
	return nil
}

// PushWatermark 接收一条水位线；非严格递增时整体拒绝且不留痕。
func (b *Buffer) PushWatermark(seq int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.hasLastWM && seq <= b.lastWM {
		b.logf("reject watermark seq=%d reason=%q last=%d",
			seq, errText(ErrWatermarkNotIncreasing), b.lastWM)
		return ErrWatermarkNotIncreasing
	}

	e := &entry{kind: WatermarkEvent, seq: seq, order: b.orderSeq, seg: b.openSeg}
	b.orderSeq++
	b.entries = append(b.entries, e)
	// 该水位线是第 openSeg 段的右屏障；它开启下一段的计数。
	barrierSeg := b.openSeg
	if b.mode == Unordered {
		for len(b.segPending) <= barrierSeg {
			b.segPending = append(b.segPending, 0)
		}
		b.openSeg++
	}
	b.hasLastWM = true
	b.lastWM = seq
	b.logf("push watermark seq=%d order=%d accepted", seq, e.order)

	b.pump()
	return nil
}

// Complete 声明某元素的异步请求已完成。
func (b *Buffer) Complete(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	// 校验顺序固定：未知标识 → 已完成。拒绝不改变任何状态。
	e, ok := b.live[id]
	if !ok {
		b.logf("reject complete id=%s reason=%q", id, errText(ErrUnknownID))
		return ErrUnknownID
	}
	if e.done {
		b.logf("reject complete id=%s reason=%q", id, errText(ErrAlreadyCompleted))
		return ErrAlreadyCompleted
	}

	e.done = true
	e.doneOrder = b.doneSeq
	b.doneSeq++
	b.logf("complete id=%s order=%d done_order=%d accepted", id, e.order, e.doneOrder)

	b.pump()
	return nil
}

// Output 查询当前可输出的事件（元素按模式排序，水位线作为屏障），
// 返回事件的副本；没有可输出事件时返回 nil。
func (b *Buffer) Output() []Event {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.outQ) == 0 {
		b.logf("output query => 0 event")
		return nil
	}
	out := make([]Event, len(b.outQ))
	copy(out, b.outQ)
	b.outQ = nil
	b.logf("output query => %d event(s) first_order=%d", len(out), out[0].Order)
	return out
}

// InFlight 返回当前占用容量的元素数（已接收但尚未输出）。
func (b *Buffer) InFlight() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.live)
}

// emit 把一个事件移入输出队列，并释放元素占用的容量。
func (b *Buffer) emit(e *entry, reason string) {
	ev := Event{Kind: e.kind, ID: e.id, Seq: e.seq, Order: e.order}
	b.outQ = append(b.outQ, ev)
	if e.kind == ElementEvent {
		delete(b.live, e.id)
	}
	b.logf("emit %s order=%d reason=%s", e.desc(), e.order, reason)
}

// pump 在每次状态变更后，按模式把所有已满足输出条件的事件
// 立即判定输出（级联），直到没有新的事件可输出。
func (b *Buffer) pump() {
	if b.mode == Ordered {
		b.pumpOrdered()
		return
	}
	b.pumpUnordered()
}

// pumpOrdered 有序模式：队头是已完成元素或水位线即输出，
// 严格保持输入顺序。
func (b *Buffer) pumpOrdered() {
	for len(b.entries) > 0 {
		head := b.entries[0]
		switch {
		case head.kind == WatermarkEvent:
			b.entries = b.entries[1:]
			b.emit(head, `"head is watermark"`)
		case head.done:
			b.entries = b.entries[1:]
			b.emit(head, `"head element completed"`)
		default:
			b.logf("hold ordered head id=%s reason=%q", head.id, "head element not completed")
			return
		}
	}
}
