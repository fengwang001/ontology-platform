// Package barrieralign 实现双输入算子的检查点屏障对齐。
package barrieralign

import (
	"fmt"
	"sync"
)

// Event 表示到达其中一个输入通道的单条事件：要么是一条记录，要么是一道屏障。
// Barrier 为 0 时表示记录（此时 Key 必须非空）；非 0 时表示屏障。
type Event struct {
	Channel int
	Key     string
	Value   []byte
	Barrier int64
}

// OutputEvent 是向下游转发的事件，语义与 Event 相同并额外带有对齐标记。
type OutputEvent struct {
	Kind    string // "record" 或 "barrier"
	Channel int
	Key     string
	Value   []byte
	Barrier int64
	Aligned bool
}

// Snapshot 是两通道在同一编号屏障处对齐时拍下的逐键一致快照。
type Snapshot struct {
	Barrier int64
	Counts  map[string]int
}

// RejectKind 枚举拒绝原因。
type RejectKind string

const (
	RejectInvalidChannel    RejectKind = "invalid_channel"
	RejectEmptyKey          RejectKind = "empty_key"
	RejectUnexpectedBarrier RejectKind = "unexpected_barrier"
	RejectBufferOverflow    RejectKind = "buffer_overflow"
)

// RejectError 携带可区分的拒绝原因与定位信息。
type RejectError struct {
	Kind    RejectKind
	Index   int // 事件在被拒绝批次中的下标（单条调用时为 0）
	Channel int
	Barrier int64
	Want    int64
	Limit   int
}

func (e *RejectError) Error() string {
	switch e.Kind {
	case RejectInvalidChannel:
		return fmt.Sprintf("barrieralign: 非法通道号 %d（只允许 0 或 1）", e.Channel)
	case RejectEmptyKey:
		return fmt.Sprintf("barrieralign: 通道 %d 的记录键为空（批内第 %d 条）", e.Channel, e.Index)
	case RejectUnexpectedBarrier:
		return fmt.Sprintf("barrieralign: 通道 %d 的屏障编号 %d 不等于期望的下一个编号 %d（批内第 %d 条）",
			e.Channel, e.Barrier, e.Want, e.Index)
	case RejectBufferOverflow:
		return fmt.Sprintf("barrieralign: 通道 %d 缓冲将超过上限 %d（批内第 %d 条）", e.Channel, e.Limit, e.Index)
	default:
		return "barrieralign: 未知拒绝原因"
	}
}

// Aligner 是并发安全的双输入屏障对齐器。
type Aligner struct {
	mu    sync.RWMutex
	core  *core
	limit int
}

// New 创建对齐器，bufferLimit 为对齐期间每个通道允许缓冲的最大记录数。
// 被阻塞通道每多缓冲一条记录前都会检查该上限；非正数会被规整为 1。
func New(bufferLimit int) *Aligner {
	if bufferLimit <= 0 {
		bufferLimit = 1
	}
	return &Aligner{core: newCore(), limit: bufferLimit}
}

// Process 处理单条事件；被拒绝时内部状态保持不变。
func (a *Aligner) Process(ev Event) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.commitOne(a.core, ev, 0)
}

// ProcessBatch 原子地处理一批事件：任一事件非法则整批拒绝，
// 累加状态、缓冲、快照与输出流均不发生任何变化。
func (a *Aligner) ProcessBatch(events []Event) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	candidate := a.core.clone()
	for i, ev := range events {
		if err := a.commitOne(candidate, ev, i); err != nil {
			return err // candidate 随返回丢弃，已提交的 a.core 原封不动
		}
	}
	a.core = candidate
	return nil
}

// Outputs 返回自启动以来转发给下游的全部事件的副本。
func (a *Aligner) Outputs() []OutputEvent {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]OutputEvent, len(a.core.out))
	for i, o := range a.core.out {
		o.Value = cloneBytes(o.Value)
		out[i] = o
	}
	return out
}

// Snapshots 返回已完成对齐的全部快照，按屏障编号升序。
func (a *Aligner) Snapshots() []Snapshot {
	a.mu.RLock()
	defer a.mu.RUnlock()
	snaps := make([]Snapshot, len(a.core.snaps))
	for i, s := range a.core.snaps {
		m := make(map[string]int, len(s.Counts))
		for k, v := range s.Counts {
			m[k] = v
		}
		snaps[i] = Snapshot{Barrier: s.Barrier, Counts: m}
	}
	return snaps
}

// Pending 返回两通道当前缓冲中的记录数。
func (a *Aligner) Pending() (int, int) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.core.ch[0].buffered, a.core.ch[1].buffered
}

// commitOne 在给定状态上处理一条事件，成功时就地修改状态。
func (a *Aligner) commitOne(c *core, ev Event, index int) error {
	if ev.Channel != 0 && ev.Channel != 1 {
		return &RejectError{Kind: RejectInvalidChannel, Index: index, Channel: ev.Channel}
	}
	ch := &c.ch[ev.Channel]
	isBarrier := ev.Barrier != 0

	if !isBarrier && ev.Key == "" {
		return &RejectError{Kind: RejectEmptyKey, Index: index, Channel: ev.Channel}
	}
	if isBarrier && ev.Barrier != ch.nextBarrier {
		return &RejectError{
			Kind:    RejectUnexpectedBarrier,
			Index:   index,
			Channel: ev.Channel,
			Barrier: ev.Barrier,
			Want:    ch.nextBarrier,
		}
	}

	// 未阻塞：记录到达即累加并转发；屏障则立即标记本通道阻塞。
	if !ch.blocked {
		if isBarrier {
			ch.blocked = true
			ch.nextBarrier = ev.Barrier + 1
			return a.tryAlign(c, ev.Barrier)
		}
		c.counts[ev.Key]++
		c.out = append(c.out, OutputEvent{
			Kind:    "record",
			Channel: ev.Channel,
			Key:     ev.Key,
			Value:   cloneBytes(ev.Value),
		})
		return nil
	}

	// 已阻塞：记录与后续屏障全部按到达顺序进入缓冲队列。
	if !isBarrier && ch.buffered >= a.limit {
		return &RejectError{
			Kind:    RejectBufferOverflow,
			Index:   index,
			Channel: ev.Channel,
			Limit:   a.limit,
		}
	}
	if !isBarrier {
		ch.buffered++
	}
	c.queue = append(c.queue, item{
		channel: ev.Channel,
		key:     ev.Key,
		value:   cloneBytes(ev.Value),
		barrier: ev.Barrier,
	})
	return nil
}

// tryAlign 在两道同号屏障都到达时拍快照、转发屏障并重放缓冲。
func (a *Aligner) tryAlign(c *core, n int64) error {
	if !c.ch[0].blocked || !c.ch[1].blocked {
		return nil // 另一通道尚未到达屏障，继续等待对齐
	}

	// 两通道均阻塞于同号屏障：此刻累加计数恰好包含两侧屏障之前的全部记录。
	snap := Snapshot{Barrier: n, Counts: make(map[string]int, len(c.counts))}
	for k, v := range c.counts {
		snap.Counts[k] = v
	}
	c.snaps = append(c.snaps, snap)

	// 屏障向下游转发。
	c.out = append(c.out, OutputEvent{Kind: "barrier", Barrier: n, Aligned: true})

	// 两通道解除阻塞，然后按全局到达顺序重放队列；
	// 遇到任一侧的下一号屏障即重新阻塞并停止重放。
	c.ch[0].blocked = false
	c.ch[1].blocked = false

	replayed := 0
loop:
	for _, it := range c.queue {
		ch := &c.ch[it.channel]
		if it.isBarrier() {
			if it.barrier != ch.nextBarrier {
				return &RejectError{
					Kind:    RejectUnexpectedBarrier,
					Channel: it.channel,
					Barrier: it.barrier,
					Want:    ch.nextBarrier,
				}
			}
			ch.blocked = true
			ch.nextBarrier = it.barrier + 1
			replayed++
			break loop
		}
		ch.buffered--
		c.counts[it.key]++
		c.out = append(c.out, OutputEvent{
			Kind:    "record",
			Channel: it.channel,
			Key:     it.key,
			Value:   cloneBytes(it.value),
		})
		replayed++
	}
	// 已重放的前缀（含终止屏障）移除；未遇到下一号屏障时队列整体清空。
	c.queue = c.queue[replayed:]
	return nil
}
