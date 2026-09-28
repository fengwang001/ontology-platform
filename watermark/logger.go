package watermark

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// Logger 包装 Buffer，在每次操作时向指定输出写入结构化日志：
// 输入事件、判定结果与判定依据、主输出与旁路输出快照。
//
// Logger 的方法同样并发安全：Buffer 自身保证状态线性化，Logger
// 额外以锁串行化日志写入，避免行内容交错。
type Logger struct {
	buf *Buffer

	mu sync.Mutex
	w  io.Writer
}

// NewLogger 创建包装 buf 的日志器；w 为 nil 时输出到标准错误。
func NewLogger(buf *Buffer, w io.Writer) *Logger {
	if w == nil {
		w = os.Stderr
	}
	return &Logger{buf: buf, w: w}
}

// Offer 调用底层 Buffer.Offer 并记录日志，返回原始结果。
func (l *Logger) Offer(e Event) OfferResult {
	res := l.buf.Offer(e)

	l.mu.Lock()
	defer l.mu.Unlock()

	fmt.Fprintf(l.w, "[watermark] input  id=%q time=%d\n", e.ID, e.Time)
	fmt.Fprintf(l.w, "[watermark] decide %s seq=%s watermark=%d basis=%s\n",
		res.Outcome, seqOrDash(res.Seq), res.Watermark, l.basis(e, res))
	if len(res.Released) > 0 {
		fmt.Fprintf(l.w, "[watermark] released-now %s\n", formatEvents(res.Released))
	}
	fmt.Fprintf(l.w, "[watermark] output main=%s\n", formatEvents(l.buf.MainOutput()))
	fmt.Fprintf(l.w, "[watermark] output side=%s\n", formatEvents(l.buf.SideOutput()))
	return res
}

// Close 调用底层 Buffer.Close，记录尾批排空日志并返回尾批事件。
func (l *Logger) Close() []Event {
	tail := l.buf.Close()

	l.mu.Lock()
	defer l.mu.Unlock()

	fmt.Fprintf(l.w, "[watermark] close  tail=%s\n", formatEvents(tail))
	fmt.Fprintf(l.w, "[watermark] output main=%s\n", formatEvents(l.buf.MainOutput()))
	fmt.Fprintf(l.w, "[watermark] output side=%s\n", formatEvents(l.buf.SideOutput()))
	return tail
}

// Buffer 返回被包装的底层缓冲。
func (l *Logger) Buffer() *Buffer { return l.buf }

// basis 给出判定的可复核依据。
func (l *Logger) basis(e Event, res OfferResult) string {
	switch res.Outcome {
	case OutcomeAccepted:
		if len(res.Released) > 0 {
			return fmt.Sprintf("accepted; water advanced to %d; flushed buffered events with time < %d", res.Watermark, res.Watermark)
		}
		return fmt.Sprintf("accepted; buffered (time %d >= watermark %d)", e.Time, res.Watermark)
	case OutcomeLate:
		return fmt.Sprintf("event time %d < watermark %d => late, routed to side output", e.Time, res.Watermark)
	case OutcomeBufferFull:
		return "rejected; pending count reached capacity, no state changed"
	case OutcomeDuplicateID, OutcomeEmptyID, OutcomeInvalidParameter, OutcomeClosed:
		return "rejected; " + res.Reason + "; no state changed"
	default:
		return res.Reason
	}
}

func seqOrDash(seq int64) string {
	if seq == 0 {
		return "-"
	}
	return fmt.Sprintf("%d", seq)
}

// formatEvents 将事件列表格式化为 "(seq 未知时省略) id@time" 的紧凑形式。
// 主/旁路快照本身不含序号，故仅输出 id 与时间。
func formatEvents(events []Event) string {
	if len(events) == 0 {
		return "[]"
	}
	parts := make([]string, len(events))
	for i, e := range events {
		parts[i] = fmt.Sprintf("%s@%d", e.ID, e.Time)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
