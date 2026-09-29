// Package asyncio 提供异步输入输出算子的有序与无序输出缓冲。
//
// 缓冲接收元素（携带调用方声明的唯一标识）与严格递增的水位线交织的输入流，
// 元素的异步处理可能乱序完成；缓冲负责在有序（Ordered）与无序（Unordered）
// 两种模式下决定元素与水位线的最终输出顺序。
//
// 并发模型：所有方法均通过同一把互斥锁串行化内部状态变更，PushElement、
// PushWatermark、Complete、Drain/PopOutputs 与 Stats 可以被多个执行体并发
// 调用。Drain 向调用方 channel 发送时不持锁，因而不会因消费者慢而阻塞
// 生产者；并发 Drain 之间通过事件值比较保证每条输出恰好被取走一次。
package asyncio

import (
	"context"
	"log/slog"
	"sync"
)

// Mode 决定缓冲的输出顺序规则。
type Mode int

const (
	// Ordered 有序模式：输出顺序严格等于元素/水位线的输入顺序。
	Ordered Mode = iota
	// Unordered 无序模式：相邻水位线把流划分为段落，已开放段落内的元素
	// 完成即可输出，未开放段落内完成的元素被扣住，直到前序水位线输出后
	// 级联开放并按完成先后释放。
	Unordered
)

// String 返回模式名称。
func (m Mode) String() string {
	switch m {
	case Ordered:
		return "ordered"
	case Unordered:
		return "unordered"
	default:
		return "invalid"
	}
}

// EventKind 区分输出事件是元素还是水位线。
type EventKind int

const (
	// KindElement 元素输出事件。
	KindElement EventKind = iota
	// KindWatermark 水位线输出事件。
	KindWatermark
)

// Event 是缓冲产生的一条输出。
type Event struct {
	// Kind 为 KindElement 时携带 ID/Value；为 KindWatermark 时携带 Watermark。
	Kind      EventKind
	ID        string
	Value     any
	Watermark int64
	// Seq 是该事件在输入流中的全局序号（从 0 起，元素与水位线统一编号）。
	Seq int
}

// Stats 是缓冲的瞬时占用快照。
type Stats struct {
	// InFlight 是已接收但尚未声明完成的元素数，占用容量的正是它。
	InFlight int
	// Capacity 是构造时给定的容量上限。
	Capacity int
	// Buffered 是已具备输出条件但尚未被 PopOutputs/Drain 取走的事件数。
	Buffered int
	// Pending 是队列中尚未输出的条目数（含未完成元素、被扣元素与水位线）。
	Pending int
}

// Buffer 是并发安全的异步输出缓冲，零值不可用，必须用 New 构造。
type Buffer struct {
	mu       sync.Mutex
	cond     *sync.Cond
	mode     Mode
	capacity int
	logger   *slog.Logger

	// seq 为输入流中元素与水位线统一编号。
	seq int
	// doneOrder 为完成声明的全局先后编号，用于无序模式被扣元素的释放排序。
	doneOrder int
	inFlight  int

	// entries 保持输入顺序的逻辑队列；无序模式中段内元素输出后会被物理移除。
	entries []*entry
	// active 保存所有“仍在 entries 中”的元素，完成或输出后删除。
	active map[string]*entry

	lastWM    int64
	haveLastW bool

	// pushSeg：已接收的水位线数，也是新元素归属的段号（第 k 条水位线之后的
	// 元素属于段 k）。openSeg：已输出的水位线数；段 k 开放当且仅当 k <= openSeg，
	// 初始段 0 恒开放。水位线输出时 openSeg++，级联开放后续段。
	pushSeg int
	openSeg int

	// ready 是已判定输出、等待消费者取走的事件队列。
	ready []Event
}

type entryKind int

const (
	eElement entryKind = iota
	eWatermark
)

type entry struct {
	kind entryKind
	seq  int
	// pos 是该条目在输入流中的位置序号（与 seq 相同），用于判定元素与水位线的先后。
	pos int

	// 元素字段
	id        string
	value     any
	completed bool
	emitted   bool
	seg       int
	doneAt    int // 完成声明的先后编号（0 表示尚未完成）

	// 水位线字段
	wm      int64
	pending int // 输入顺序中位于该水位线之前、尚未输出的元素数
}

// New 创建一个容量为 capacity、按 mode 规则输出的缓冲。
// capacity 必须大于 0，否则返回 ErrInvalidCapacity；logger 为 nil 时用默认 logger。
func New(mode Mode, capacity int, logger *slog.Logger) (*Buffer, error) {
	if mode != Ordered && mode != Unordered {
		return nil, ErrInvalidMode
	}
	if capacity <= 0 {
		return nil, ErrInvalidCapacity
	}
	if logger == nil {
		logger = slog.Default()
	}
	b := &Buffer{
		mode:     mode,
		capacity: capacity,
		logger:   logger,
		active:   make(map[string]*entry),
	}
	b.cond = sync.NewCond(&b.mu)
	return b, nil
}

// PushElement 接收一个元素。容量已满、标识为空或标识与在队元素重复时整体拒绝，
// 拒绝不改变队列、占用数或已产生输出。
func (b *Buffer) PushElement(id string, value any) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	// 校验顺序：空标识 -> 重复标识 -> 容量已满，任何一项失败都在状态变更前返回。
	if id == "" {
		b.reject("push_element", ErrEmptyID, slog.String("id", id))
		return ErrEmptyID
	}
	if _, dup := b.active[id]; dup {
		b.reject("push_element", ErrDuplicateID, slog.String("id", id))
		return ErrDuplicateID
	}
	if b.inFlight >= b.capacity {
		b.reject("push_element", ErrCapacityFull,
			slog.String("id", id),
			slog.Int("in_flight", b.inFlight),
			slog.Int("capacity", b.capacity))
		return ErrCapacityFull
	}

	seq := b.takeSeqLocked()
	e := &entry{
		kind:  eElement,
		seq:   seq,
		pos:   seq,
		id:    id,
		value: value,
		seg:   b.pushSeg,
	}
	b.entries = append(b.entries, e)
	b.active[id] = e
	b.inFlight++

	b.logger.LogAttrs(context.Background(), slog.LevelInfo, "push_element accepted",
		slog.String("id", id),
		slog.Int("seq", e.seq),
		slog.Int("seg", e.seg),
		slog.Int("in_flight", b.inFlight),
		slog.Int("capacity", b.capacity),
		slog.String("mode", b.mode.String()),
	)
	return nil
}

// PushWatermark 接收一个水位线，必须相对上一条水位线严格递增，否则整体拒绝。
func (b *Buffer) PushWatermark(wm int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.haveLastW && wm <= b.lastWM {
		b.reject("push_watermark", ErrWatermarkNotIncreasing,
			slog.Int64("watermark", wm),
			slog.Int64("last", b.lastWM))
		return ErrWatermarkNotIncreasing
	}

	pending := 0
	for _, e := range b.entries {
		if e.kind == eElement {
			pending++
		}
	}
	seq := b.takeSeqLocked()
	e := &entry{
		kind:    eWatermark,
		seq:     seq,
		pos:     seq,
		wm:      wm,
		pending: pending,
	}
	b.entries = append(b.entries, e)
	b.lastWM = wm
	b.haveLastW = true

	// 水位线之后到达的元素归入下一段（尚未开放）。
	newSeg := b.pushSeg + 1

	b.logger.LogAttrs(context.Background(), slog.LevelInfo, "push_watermark accepted",
		slog.Int64("watermark", wm),
		slog.Int("seq", e.seq),
		slog.Int("pending_before", pending),
		slog.Int("next_seg", newSeg),
		slog.String("mode", b.mode.String()),
	)

	b.pushSeg = newSeg
	b.drainLocked("after_push_watermark")
	return nil
}

// Complete 由调用方声明某元素的异步处理已经完成。未知标识、空标识或对已完成
// 标识重复声明都会被拒绝。完成声明先释放占用，再按模式规则尽可能级联产出输出。
func (b *Buffer) Complete(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if id == "" {
		b.reject("complete", ErrEmptyID, slog.String("id", id))
		return ErrEmptyID
	}
	e, ok := b.active[id]
	if !ok {
		b.reject("complete", ErrUnknownID, slog.String("id", id))
		return ErrUnknownID
	}
	if e.completed {
		b.reject("complete", ErrAlreadyCompleted, slog.String("id", id))
		return ErrAlreadyCompleted
	}

	e.completed = true
	b.doneOrder++
	e.doneAt = b.doneOrder
	b.inFlight--

	b.logger.LogAttrs(context.Background(), slog.LevelInfo, "complete accepted",
		slog.String("id", id),
		slog.Int("seq", e.seq),
		slog.Int("seg", e.seg),
		slog.Int("done_at", e.doneAt),
		slog.Int("in_flight", b.inFlight),
		slog.String("mode", b.mode.String()),
	)

	b.drainLocked("after_complete")
	return nil
}

// Drain 把已就绪的输出逐条送入 ch：暂无输出时阻塞等待，直到有新输出或 ctx
// 取消，返回本次送出的条数。可被多个执行体并发调用；每条事件恰好取走一次。
// 调用方应通过取消 ctx 终止 Drain，而不是关闭 ch（关闭中的 channel 不应再写入）。
func (b *Buffer) Drain(ctx context.Context, ch chan<- Event) int {
	ctxDone := make(chan struct{})
	defer close(ctxDone)
	go func() {
		select {
		case <-ctx.Done():
			b.cond.Broadcast()
		case <-ctxDone:
		}
	}()

	sent := 0
	for {
		b.mu.Lock()
		for len(b.ready) == 0 && ctx.Err() == nil {
			// 等待新的完成/输入推进输出，或 ctx 取消的广播。
			b.cond.Wait()
		}
		if ctx.Err() != nil {
			b.mu.Unlock()
			return sent
		}
		// 先在锁内出队，保证并发 Drain 不会把同一事件发送两次。
		ev := b.ready[0]
		b.ready = b.ready[1:]
		b.mu.Unlock()

		// ctx 取消而事件尚未送达时，把它回置队列并唤醒其他 Drain，避免丢失。
		select {
		case ch <- ev:
		case <-ctx.Done():
			b.mu.Lock()
			b.ready = append([]Event{ev}, b.ready...)
			b.cond.Broadcast()
			b.mu.Unlock()
			return sent
		}

		sent++
	}
}

// PopOutputs 取出并移除当前已就绪的全部输出（便于同步测试与轮询使用）。
func (b *Buffer) PopOutputs() []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.ready
	b.ready = nil
	if len(out) == 0 {
		return nil
	}
	b.logger.LogAttrs(context.Background(), slog.LevelDebug, "pop_outputs",
		slog.Int("count", len(out)))
	return out
}

// SnapshotOutputs 返回当前已就绪输出的副本，不移除。
func (b *Buffer) SnapshotOutputs() []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.ready) == 0 {
		return nil
	}
	out := make([]Event, len(b.ready))
	copy(out, b.ready)
	return out
}

// Stats 返回占用与队列快照。
func (b *Buffer) Stats() Stats {
	b.mu.Lock()
	defer b.mu.Unlock()
	return Stats{
		InFlight: b.inFlight,
		Capacity: b.capacity,
		Buffered: len(b.ready),
		Pending:  len(b.entries),
	}
}

func (b *Buffer) takeSeqLocked() int {
	s := b.seq
	b.seq++
	return s
}

func (b *Buffer) reject(op string, err error, attrs ...slog.Attr) {
	all := make([]slog.Attr, 0, len(attrs)+1)
	all = append(all, slog.String("reason", err.Error()))
	all = append(all, attrs...)
	b.logger.LogAttrs(context.Background(), slog.LevelWarn, op+" rejected", all...)
}

func sameEvent(a, b Event) bool {
	if a.Kind != b.Kind || a.Seq != b.Seq {
		return false
	}
	if a.Kind == KindElement {
		return a.ID == b.ID
	}
	return a.Watermark == b.Watermark
}

// drainLocked 按当前模式尽可能把输出推进到 ready。
func (b *Buffer) drainLocked(reason string) {
	if b.mode == Ordered {
		b.drainOrderedLocked(reason)
		return
	}
	// 无序：每回合先放行满足条件的队头水位线（开放新段），再扫描所有开放段内
	// 已完成的元素；重复直到两者都无进展，形成屏障与被扣元素的级联释放。
	for {
		if b.openBarriersLocked(reason) {
			continue
		}
		if b.emitOpenSegmentLocked() {
			continue
		}
		return
	}
}

// drainOrderedLocked 有序模式：队头是已完成元素或水位线即输出，
// 连续输出直到队头被未完成元素阻塞。
func (b *Buffer) drainOrderedLocked(reason string) {
	for len(b.entries) > 0 {
		head := b.entries[0]
		if head.kind == eElement {
			if !head.completed {
				return
			}
			b.emitElementLocked(head, "head_completed")
			b.popHeadLocked()
			continue
		}
		if head.pending != 0 {
			return
		}
		b.emitWatermarkLocked(head, reason)
		b.popHeadLocked()
	}
}

// emitOpenSegmentLocked 输出当前开放段（seg <= openSeg）内已完成但尚未输出的
// 元素：其前面的水位线均已输出，因而可立即输出；输出顺序按完成声明先后。
// 返回本回合是否输出了至少一条元素。
func (b *Buffer) emitOpenSegmentLocked() bool {
	var best *entry
	for _, e := range b.entries {
		if e.kind != eElement || e.emitted || !e.completed || e.seg > b.openSeg {
			continue
		}
		if best == nil || e.doneAt < best.doneAt {
			best = e
		}
	}
	if best == nil {
		return false
	}
	b.emitElementLocked(best, "open_segment_completion")
	b.removeEntryLocked(best)
	return true
}

// openBarriersLocked 从队头起，凡 pending==0 的水位线立即输出并开放下一段；
// 每开放一段，先按完成先后释放该段被扣住的元素，再检查后续水位线，形成级联。
// openBarriersLocked 放行一条（仅一条）可放行的队头水位线并开放其守护段，
// 返回是否发生了放行。主循环随后重新扫描，形成级联。
func (b *Buffer) openBarriersLocked(reason string) bool {
	if len(b.entries) == 0 {
		return false
	}
	head := b.entries[0]
	if head.kind != eWatermark || head.pending != 0 {
		return false
	}
	b.openSeg++ // 该水位线守护的段号 == 新的 openSeg
	b.emitWatermarkLocked(head, reason)
	b.popHeadLocked()
	return true
}

// emitElementLocked 把元素判定为输出：加入 ready、移出 active，并把仍在队中的
// 每条水位线的前置未输出计数减一（该元素位于所有这些水位线之前）。
func (b *Buffer) emitElementLocked(e *entry, reason string) {
	if e.emitted {
		return
	}
	e.emitted = true
	b.ready = append(b.ready, Event{
		Kind:  KindElement,
		ID:    e.id,
		Value: e.value,
		Seq:   e.seq,
	})
	delete(b.active, e.id)

	for _, w := range b.entries {
		// 只有排在此元素之后的水位线才把它算作“之前尚未输出的元素”；
		// 无序模式中元素可能先于其后水位线被物理移除，绝不能递减后面的计数。
		if w.kind == eWatermark && !w.emitted && w.pos > e.pos {
			w.pending--
		}
	}

	b.logger.LogAttrs(context.Background(), slog.LevelInfo, "emit element",
		slog.String("id", e.id),
		slog.Int("seq", e.seq),
		slog.Int("seg", e.seg),
		slog.Int("done_at", e.doneAt),
		slog.String("reason", reason),
		slog.String("mode", b.mode.String()),
		slog.Int("ready", len(b.ready)),
	)
	b.cond.Broadcast()
}

// emitWatermarkLocked 把水位线判定为输出。
func (b *Buffer) emitWatermarkLocked(e *entry, reason string) {
	e.emitted = true
	b.ready = append(b.ready, Event{
		Kind:      KindWatermark,
		Watermark: e.wm,
		Seq:       e.seq,
	})
	b.logger.LogAttrs(context.Background(), slog.LevelInfo, "emit watermark",
		slog.Int64("watermark", e.wm),
		slog.Int("seq", e.seq),
		slog.String("reason", reason),
		slog.String("mode", b.mode.String()),
		slog.Int("ready", len(b.ready)),
	)
	b.cond.Broadcast()
}

func (b *Buffer) popHeadLocked() {
	b.entries = b.entries[1:]
}

// removeEntryLocked 从 entries 中物理移除指定条目（条目为堆指针，外部引用仍有效）。
func (b *Buffer) removeEntryLocked(target *entry) {
	for i, e := range b.entries {
		if e == target {
			b.entries = append(b.entries[:i], b.entries[i+1:]...)
			return
		}
	}
}
