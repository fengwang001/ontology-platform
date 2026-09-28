// Package reorder 实现有界乱序（bounded out-of-orderness）的事件时间重排缓冲。
//
// 乱序到达的事件按事件时间进入缓冲，水位线推进时把到期事件按（事件时间, 到达序号）
// 稳定排序后从主输出交出；时间不超过当前水位线的迟到事件立即进入旁路输出。
// 所有方法均可被并发调用。
package reorder

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
)

// 可区分的拒绝原因，调用方可用 errors.Is 判断，或用 ReasonOf 取得字符串形式的原因码。
var (
	// ErrInvalidParam 构造参数非法或事件字段非法（如负的事件时间）。
	ErrInvalidParam = errors.New("reorder: invalid parameter")
	// ErrEmptyID 事件标识为空。
	ErrEmptyID = errors.New("reorder: empty event id")
	// ErrDuplicateID 事件标识与此前已接受的事件重复。
	ErrDuplicateID = errors.New("reorder: duplicate event id")
	// ErrBufferFull 事件需要进入缓冲但缓冲已达容量上限。
	ErrBufferFull = errors.New("reorder: buffer capacity exceeded")
)

// RejectReason 是拒绝原因的字符串码，便于日志与测试断言。
type RejectReason string

// 拒绝原因码，与对应的 sentinel 错误一一对应。
const (
	ReasonInvalidParam RejectReason = "invalid_param"
	ReasonEmptyID      RejectReason = "empty_id"
	ReasonDuplicateID  RejectReason = "duplicate_id"
	ReasonBufferFull   RejectReason = "buffer_full"
)

// Event 是进入重排缓冲的事件。
type Event struct {
	// ID 事件唯一标识，不允许为空，不允许在同一 Buffer 生命周期内重复。
	ID string
	// Time 事件时间（如 epoch 毫秒），必须 >= 0。
	Time int64
	// Payload 业务负载，可为任意类型，缓冲不解释其内容。
	Payload any
}

// acceptedEvent 是缓冲内部记录：事件 + 连续到达序号。
type acceptedEvent struct {
	event Event
	seq   uint64
}

// Buffer 是并发安全的有界乱序重排缓冲。
//
// 零值不可用，必须通过 NewBuffer 构造。
type Buffer struct {
	mu sync.Mutex

	capacity          int
	maxOutOfOrderness int64
	logger            *slog.Logger

	// watermark 为当前水位线；尚未见到任何事件时为 -1，
	// 表示“时间 <= watermark 的事件均属迟到”。
	watermark int64
	// maxEventTime 为已接受事件中见到的最大事件时间（初值 -1）。
	maxEventTime int64
	// nextSeq 为下一个被接受事件的到达序号，从 1 开始连续分配。
	nextSeq uint64

	// buffered 为尚未到期、仍在缓冲中的事件。
	buffered []acceptedEvent
	// seen 记录所有已接受事件（含旁路事件）的标识，用于去重。
	seen map[string]struct{}

	// mainOutput 为主输出：按（时间, 序号）严格有序的已释放事件。
	mainOutput []acceptedEvent
	// sideOutput 为旁路输出：按到达顺序记录的迟到事件。
	sideOutput []acceptedEvent
}

// NewBuffer 构造一个重排缓冲。
//
// capacity 为缓冲可同时持有的事件数上限，必须 >= 1；
// maxOutOfOrderness 为允许的乱序跨度（事件时间单位），必须 >= 0，
// 水位线 = 迄今为止见到的最大事件时间 - maxOutOfOrderness，单调不减；
// logger 为 nil 时使用 slog 默认 logger。
func NewBuffer(capacity int, maxOutOfOrderness int64, logger *slog.Logger) (*Buffer, error) {
	if capacity < 1 {
		return nil, fmt.Errorf("%w: capacity must be >= 1, got %d", ErrInvalidParam, capacity)
	}
	if maxOutOfOrderness < 0 {
		return nil, fmt.Errorf("%w: maxOutOfOrderness must be >= 0, got %d", ErrInvalidParam, maxOutOfOrderness)
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Buffer{
		capacity:          capacity,
		maxOutOfOrderness: maxOutOfOrderness,
		logger:            logger,
		watermark:         -1,
		maxEventTime:      -1,
		nextSeq:           1,
		buffered:          make([]acceptedEvent, 0, capacity),
		seen:              make(map[string]struct{}),
	}, nil
}

// Accept 接受一个事件。
//
// 事件时间不超过当前水位线即为迟到，立即进入旁路输出，并返回其到达序号；
// 否则进入缓冲，若该事件推进了水位线，则所有到期事件按（事件时间, 到达序号）
// 稳定排序后一次性释放到主输出。
//
// 返回值 seq 为该被接受事件的连续到达序号（旁路与缓冲事件统一编号，从 1 开始）。
// 任何拒绝（ErrEmptyID / ErrInvalidParam / ErrDuplicateID / ErrBufferFull）
// 都不会改变水位线、序号、缓冲或两路输出。
func (b *Buffer) Accept(e Event) (seq uint64, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// 1) 纯字段校验，不触碰任何状态。
	if e.ID == "" {
		b.logReject(e, ReasonEmptyID, "event time <= watermark check skipped")
		return 0, ErrEmptyID
	}
	if e.Time < 0 {
		b.logReject(e, ReasonInvalidParam, "event time must be >= 0")
		return 0, fmt.Errorf("%w: event time must be >= 0, got %d", ErrInvalidParam, e.Time)
	}

	// 2) 重复标识校验（在分配序号 / 改动任何状态之前）。
	if _, ok := b.seen[e.ID]; ok {
		b.logReject(e, ReasonDuplicateID, "id already accepted")
		return 0, fmt.Errorf("%w: %q", ErrDuplicateID, e.ID)
	}

	// 3) 迟到判定：时间 <= 水位线，立即进入旁路。
	if e.Time <= b.watermark {
		seq = b.nextSeq
		b.nextSeq++
		b.seen[e.ID] = struct{}{}
		entry := acceptedEvent{event: e, seq: seq}
		b.sideOutput = append(b.sideOutput, entry)
		b.logger.Info("reorder accept",
			slog.String("op", "accept"),
			slog.String("id", e.ID),
			slog.Int64("time", e.Time),
			slog.Uint64("seq", seq),
			slog.String("decision", "late_side_output"),
			slog.String("basis", fmt.Sprintf("event time %d <= watermark %d", e.Time, b.watermark)),
			slog.Int64("watermark", b.watermark),
			slog.Int("buffered", len(b.buffered)),
			slog.Int("main_total", len(b.mainOutput)),
			slog.Int("side_total", len(b.sideOutput)),
		)
		return seq, nil
	}

	// 4) 非迟到：试算新水位线与到期事件，先确认容量，再一次性提交，
	//    确保容量超限时不留下任何状态变化。
	candidateWM := e.Time - b.maxOutOfOrderness
	if candidateWM < b.watermark {
		candidateWM = b.watermark // 水位线单调不减
	}

	var due, stay []acceptedEvent
	for _, ev := range b.buffered {
		if ev.event.Time <= candidateWM {
			due = append(due, ev)
		} else {
			stay = append(stay, ev)
		}
	}
	// 释放后再放入本事件仍超出容量 -> 拒绝（水位线也不得推进）。
	if len(stay)+1 > b.capacity {
		b.logReject(e, ReasonBufferFull,
			fmt.Sprintf("buffer full: %d held after %d due released, capacity %d", len(stay), len(due), b.capacity))
		return 0, ErrBufferFull
	}

	// 5) 提交：分配序号、登记标识、压入缓冲。
	seq = b.nextSeq
	b.nextSeq++
	b.seen[e.ID] = struct{}{}
	if e.Time > b.maxEventTime {
		b.maxEventTime = e.Time
	}
	incoming := acceptedEvent{event: e, seq: seq}

	// 本事件自身也可能已到期（如 maxOutOfOrderness == 0 时立即到期）。
	if e.Time <= candidateWM {
		due = append(due, incoming)
	} else {
		stay = append(stay, incoming)
	}
	b.buffered = stay

	// 6) 到期事件按（时间, 序号）稳定排序后释放到主输出。
	slices.SortStableFunc(due, func(a, c acceptedEvent) int {
		if a.event.Time != c.event.Time {
			if a.event.Time < c.event.Time {
				return -1
			}
			return 1
		}
		if a.seq < c.seq {
			return -1
		}
		if a.seq > c.seq {
			return 1
		}
		return 0
	})
	releasedIDs := make([]string, 0, len(due))
	for _, ev := range due {
		b.mainOutput = append(b.mainOutput, ev)
		releasedIDs = append(releasedIDs, ev.event.ID)
	}
	b.watermark = candidateWM

	decision := "buffered"
	if len(due) > 0 {
		decision = "buffered_and_released_due"
	}
	b.logger.Info("reorder accept",
		slog.String("op", "accept"),
		slog.String("id", e.ID),
		slog.Int64("time", e.Time),
		slog.Uint64("seq", seq),
		slog.String("decision", decision),
		slog.String("basis", fmt.Sprintf("event time %d > old watermark; new watermark = max event time %d - allowed lag %d",
			e.Time, b.maxEventTime, b.maxOutOfOrderness)),
		slog.Int64("watermark", b.watermark),
		slog.Any("released_ids", releasedIDs),
		slog.Int("buffered", len(b.buffered)),
		slog.Int("main_total", len(b.mainOutput)),
		slog.Int("side_total", len(b.sideOutput)),
	)
	return seq, nil
}

// Flush 释放所有仍在缓冲中的事件到主输出（仍按（时间, 序号）排序）。
//
// 用于流结束时排空缓冲；不改变水位线。Flush 之后再 Accept 的事件若被主输出释放，
// 可能早于已排空事件的时间，调用方应在流结束后才调用 Flush。
func (b *Buffer) Flush() {
	b.mu.Lock()
	defer b.mu.Unlock()

	pending := slices.Clone(b.buffered)
	slices.SortStableFunc(pending, func(a, c acceptedEvent) int {
		if a.event.Time != c.event.Time {
			if a.event.Time < c.event.Time {
				return -1
			}
			return 1
		}
		if a.seq < c.seq {
			return -1
		}
		if a.seq > c.seq {
			return 1
		}
		return 0
	})
	releasedIDs := make([]string, 0, len(pending))
	for _, ev := range pending {
		b.mainOutput = append(b.mainOutput, ev)
		releasedIDs = append(releasedIDs, ev.event.ID)
	}
	b.buffered = b.buffered[:0]
	b.logger.Info("reorder flush",
		slog.String("op", "flush"),
		slog.Any("released_ids", releasedIDs),
		slog.Int64("watermark", b.watermark),
		slog.Int("main_total", len(b.mainOutput)),
		slog.Int("side_total", len(b.sideOutput)),
	)
}

// Watermark 返回当前水位线；尚未接受任何事件时返回 -1。
func (b *Buffer) Watermark() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.watermark
}

// Pending 返回仍在缓冲中等待释放的事件副本，按（时间, 序号）排序。
func (b *Buffer) Pending() []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	pending := slices.Clone(b.buffered)
	slices.SortStableFunc(pending, func(a, c acceptedEvent) int {
		if a.event.Time != c.event.Time {
			if a.event.Time < c.event.Time {
				return -1
			}
			return 1
		}
		if a.seq < c.seq {
			return -1
		}
		if a.seq > c.seq {
			return 1
		}
		return 0
	})
	return toEvents(pending)
}

// MainOutput 返回主输出已释放事件的副本，按（时间, 序号）严格有序。
func (b *Buffer) MainOutput() []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	return toEvents(b.mainOutput)
}

// SideOutput 返回旁路（迟到）事件的副本，按到达顺序排列。
func (b *Buffer) SideOutput() []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	return toEvents(b.sideOutput)
}

// ReasonOf 返回错误对应的拒绝原因码；非本包的拒绝错误返回空字符串。
func ReasonOf(err error) RejectReason {
	switch {
	case errors.Is(err, ErrBufferFull):
		return ReasonBufferFull
	case errors.Is(err, ErrDuplicateID):
		return ReasonDuplicateID
	case errors.Is(err, ErrEmptyID):
		return ReasonEmptyID
	case errors.Is(err, ErrInvalidParam):
		return ReasonInvalidParam
	default:
		return ""
	}
}

func (b *Buffer) logReject(e Event, reason RejectReason, basis string) {
	b.logger.Info("reorder reject",
		slog.String("op", "accept"),
		slog.String("id", e.ID),
		slog.Int64("time", e.Time),
		slog.String("decision", "rejected"),
		slog.String("reason", string(reason)),
		slog.String("basis", basis),
		slog.Int64("watermark", b.watermark),
		slog.Int("buffered", len(b.buffered)),
		slog.Int("main_total", len(b.mainOutput)),
		slog.Int("side_total", len(b.sideOutput)),
	)
}

func toEvents(in []acceptedEvent) []Event {
	out := make([]Event, len(in))
	for i, ev := range in {
		out[i] = ev.event
	}
	return out
}
