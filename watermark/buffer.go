// Package watermark 实现有界乱序的事件时间重排缓冲。
//
// 乱序到达的事件在此按事件时间排序后从主输出交出；太晚到达（迟到）
// 的事件立即从旁路交出。所有操作可被并发调用，对同一输入序列具有
// 确定性结果：主输出严格按 (事件时间, 到达序号) 升序，事件不丢不重。
package watermark

import (
	"errors"
	"sort"
	"strings"
	"sync"
)

// ErrInvalidCapacity 在 NewBuffer 收到非正容量时返回。
var ErrInvalidCapacity = errors.New("watermark: capacity must be a positive integer")

// Event 是进入重排缓冲的事件。
type Event struct {
	// ID 为事件唯一标识，不得为空或纯空白，且在同一缓冲实例内不得重复。
	ID string
	// Time 为事件时间，必须为非负数。
	Time int64
}

// Outcome 是一次 Offer 调用的判定结果类别。
type Outcome int

const (
	// OutcomeInvalidParameter：事件时间等结构性参数非法。
	OutcomeInvalidParameter Outcome = iota + 1
	// OutcomeEmptyID：事件标识为空或纯空白。
	OutcomeEmptyID
	// OutcomeDuplicateID：事件标识此前已被系统接收（进入过主路或旁路）。
	OutcomeDuplicateID
	// OutcomeBufferFull：缓冲已达容量上限，事件无法进入缓冲。
	OutcomeBufferFull
	// OutcomeAccepted：事件被接受进入缓冲，并可能触发主输出释放。
	OutcomeAccepted
	// OutcomeLate：事件迟到（事件时间严格小于当前水位线），已进入旁路。
	OutcomeLate
	// OutcomeClosed：缓冲已关闭，不再接收事件。
	OutcomeClosed
)

// String 返回判定类别的可读名称。
func (o Outcome) String() string {
	switch o {
	case OutcomeInvalidParameter:
		return "INVALID_PARAMETER"
	case OutcomeEmptyID:
		return "EMPTY_ID"
	case OutcomeDuplicateID:
		return "DUPLICATE_ID"
	case OutcomeBufferFull:
		return "BUFFER_FULL"
	case OutcomeAccepted:
		return "ACCEPTED"
	case OutcomeLate:
		return "LATE"
	case OutcomeClosed:
		return "CLOSED"
	default:
		return "UNKNOWN"
	}
}

// OfferResult 是一次 Offer 调用的结果。
type OfferResult struct {
	// Outcome 为判定类别。
	Outcome Outcome
	// Reason 为可区分的人类可读原因（拒绝时非空）。
	Reason string
	// Seq 为事件被接受（进入缓冲或旁路）时分配的连续到达序号，从 1 开始；
	// 被拒绝或零值结果中为 0。
	Seq int64
	// Watermark 为本次调用完成后的水位线（仅观测用）。
	Watermark int64
	// Released 为本次水位线推进后从主输出释放的事件，按
	// (事件时间, 到达序号) 升序排列；为独立副本，调用方可自由修改。
	Released []Event
}

// buffered 是缓冲内部条目：事件与其到达序号配对，作为稳定排序键。
type buffered struct {
	event Event
	seq   int64
}

// Buffer 是有界乱序事件时间重排缓冲。
//
// 所有方法均为并发安全：内部以单一互斥锁线性化所有操作，因此每个
// 被接受事件恰好获得一个到达序号、恰好出现在主输出或旁路之一中。
type Buffer struct {
	mu sync.Mutex

	capacity int

	// seq 为已分配的最大到达序号（Accepted 与 Late 都会分配）。
	seq int64
	// wm 为当前水位线，随已接受事件的最大事件时间单调不减。
	wm int64

	// pending 为尚在缓冲中、等待水位线越过其时间的事件。
	// 不变量：其中每个事件的 Time 都 >= 当前 wm。
	pending []buffered
	// main 为主输出（已释放事件），始终按 (Time, seq) 升序。
	main []buffered
	// side 为旁路输出（迟到事件），按到达序号升序。
	side []buffered
	// seen 记录所有已被接收的事件标识（主路与旁路均登记），用于去重。
	seen map[string]struct{}

	closed bool
}

// NewBuffer 创建容量为 capacity 的重排缓冲。
// capacity 必须为正数，否则返回 ErrInvalidCapacity。
func NewBuffer(capacity int) (*Buffer, error) {
	if capacity <= 0 {
		return nil, ErrInvalidCapacity
	}
	return &Buffer{
		capacity: capacity,
		seen:     make(map[string]struct{}),
	}, nil
}

// Offer 提交一个事件并返回判定结果。
//
// 判定与状态规则：
//   - 缓冲已关闭：OutcomeClosed；
//   - Time 为负：OutcomeInvalidParameter；
//   - ID 为空或纯空白：OutcomeEmptyID；
//   - ID 已被接收过：OutcomeDuplicateID；
//   - Time 严格小于当前水位线：OutcomeLate，分配序号后立即进入旁路，
//     不占用缓冲容量、不推进水位线；
//   - 缓冲中事件数已达 capacity：OutcomeBufferFull；
//   - 其余：OutcomeAccepted，分配序号进入缓冲；若其 Time 高于水位线，
//     水位线前进到该 Time，并释放缓冲中所有 Time 严格小于新水位线的
//     事件，按 (Time, Seq) 稳定排序后交出（见结果中的 Released）。
//
// 任何拒绝都不会改变水位线、序号、缓冲或两路输出。
func (b *Buffer) Offer(e Event) OfferResult {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return OfferResult{Outcome: OutcomeClosed, Reason: "watermark: buffer is closed", Watermark: b.wm}
	}
	if e.Time < 0 {
		return OfferResult{
			Outcome:   OutcomeInvalidParameter,
			Reason:    "watermark: event time must be non-negative",
			Watermark: b.wm,
		}
	}
	if strings.TrimSpace(e.ID) == "" {
		return OfferResult{Outcome: OutcomeEmptyID, Reason: "watermark: event id must not be empty or blank", Watermark: b.wm}
	}
	if _, dup := b.seen[e.ID]; dup {
		return OfferResult{
			Outcome:   OutcomeDuplicateID,
			Reason:    "watermark: duplicate event id: " + e.ID,
			Watermark: b.wm,
		}
	}

	// 迟到：立即进入旁路。迟到不经过缓冲，因此不受缓冲容量限制。
	if e.Time < b.wm {
		b.seq++
		b.seen[e.ID] = struct{}{}
		b.side = append(b.side, buffered{event: e, seq: b.seq})
		return OfferResult{Outcome: OutcomeLate, Seq: b.seq, Watermark: b.wm}
	}

	// 准时事件：容量为硬上限，满则拒绝（不分配序号、不登记标识）。
	if len(b.pending) >= b.capacity {
		return OfferResult{
			Outcome:   OutcomeBufferFull,
			Reason:    "watermark: buffer is full",
			Watermark: b.wm,
		}
	}

	b.seq++
	seq := b.seq
	b.seen[e.ID] = struct{}{}
	b.pending = append(b.pending, buffered{event: e, seq: seq})

	var released []Event
	if e.Time > b.wm {
		b.wm = e.Time
		released = b.flushLocked()
	}
	return OfferResult{Outcome: OutcomeAccepted, Seq: seq, Watermark: b.wm, Released: released}
}

// flushLocked 释放缓冲中所有 Time 严格小于当前水位线的事件，
// 按 (Time, Seq) 稳定排序后追加到主输出，并返回同样有序的独立副本。
// 调用方必须持有 b.mu。
func (b *Buffer) flushLocked() []Event {
	ready := make([]buffered, 0, len(b.pending))
	kept := b.pending[:0]
	for _, be := range b.pending {
		if be.event.Time < b.wm {
			ready = append(ready, be)
		} else {
			kept = append(kept, be)
		}
	}
	b.pending = kept

	sort.Slice(ready, func(i, j int) bool {
		if ready[i].event.Time != ready[j].event.Time {
			return ready[i].event.Time < ready[j].event.Time
		}
		return ready[i].seq < ready[j].seq
	})

	out := eventsOf(ready)
	b.main = append(b.main, ready...)
	return out
}

// Close 排空缓冲中剩余的事件（尾批：那些 Time 等于当前水位线、
// 尚未被更高水位线释放的事件）到主输出，并返回有序的独立副本。
// 关闭后再 Offer 一律得到 OutcomeClosed；重复调用 Close 返回 nil。
// MainOutput、SideOutput、Watermark 在关闭后仍可查询。
func (b *Buffer) Close() []Event {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return nil
	}
	b.closed = true

	if len(b.pending) == 0 {
		return nil
	}

	tail := append([]buffered(nil), b.pending...)
	sort.Slice(tail, func(i, j int) bool {
		if tail[i].event.Time != tail[j].event.Time {
			return tail[i].event.Time < tail[j].event.Time
		}
		return tail[i].seq < tail[j].seq
	})

	b.main = append(b.main, tail...)
	b.pending = nil
	return eventsOf(tail)
}

// MainOutput 返回截至目前主输出的完整快照，按 (Time, 到达序号) 升序；
// 返回独立副本，调用方可自由修改。
func (b *Buffer) MainOutput() []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	return eventsOf(b.main)
}

// SideOutput 返回截至目前旁路（迟到）输出的完整快照，按到达序号升序；
// 返回独立副本，调用方可自由修改。
func (b *Buffer) SideOutput() []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	return eventsOf(b.side)
}

// mainEntries 返回主输出的 (事件, 到达序号) 有序独立副本，
// 供包内测试严格校验排序键。
func (b *Buffer) mainEntries() []buffered {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]buffered(nil), b.main...)
}

// eventsOf 提取条目列表中的事件，返回独立切片。
func eventsOf(entries []buffered) []Event {
	out := make([]Event, len(entries))
	for i, be := range entries {
		out[i] = be.event
	}
	return out
}

// Watermark 返回当前水位线；缓冲关闭前后均可调用。
func (b *Buffer) Watermark() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.wm
}
