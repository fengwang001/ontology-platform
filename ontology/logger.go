package ontology

import (
	"fmt"
	"io"
)

// TextLogger 把每次成功 Apply 的输入、输出条目与判定依据写入 io.Writer。
// 输出格式稳定，可用于审计与测试断言；同一输入序列产生完全相同的输出。
type TextLogger struct {
	w io.Writer
}

// NewTextLogger 创建写入 w 的文本日志器，w 为 nil 时丢弃日志。
func NewTextLogger(w io.Writer) *TextLogger {
	if w == nil {
		w = io.Discard
	}
	return &TextLogger{w: w}
}

// LogBatch 实现 Logger。
func (l *TextLogger) LogBatch(log BatchLog) {
	fmt.Fprintln(l.w, "=== batch begin ===")
	for i, change := range log.Changes {
		op := "INSERT"
		if change.Deleted {
			op = "DELETE"
		}
		fmt.Fprintf(l.w, "input[%d] %s row=%s group=%q value=%d\n",
			i, op, change.Row.ID, change.Row.Group, change.Row.Value)
	}
	for _, g := range log.Groups {
		fmt.Fprintf(l.w,
			"decision group=%q before{count=%d sum=%d in=%t} after{count=%d sum=%d in=%t} -> %s\n",
			g.Group,
			g.Before.Count, g.Before.Sum, g.WasIn,
			g.After.Count, g.After.Sum, g.IsIn,
			decisionText(g.Decision),
		)
		for _, e := range g.Entries {
			fmt.Fprintf(l.w, "  output %s group=%q count=%d sum=%d\n",
				e.Kind, e.Agg.Group, e.Agg.Count, e.Agg.Sum)
		}
	}
	fmt.Fprintf(l.w, "=== batch end: %d output entry(ies) ===\n", len(log.Entries))
}

func decisionText(p Presence) string {
	switch p {
	case PresenceEnter:
		return "enter (not in view -> in view, upsert only)"
	case PresenceLeave:
		return "leave (in view -> not in view, retract only)"
	case PresenceChange:
		return "change (in view -> in view, retract then upsert)"
	default:
		return "none (not in view before and after, no output)"
	}
}

// memoryLogger 保存日志副本，供需要编程式断言的调用方使用。
type memoryLogger struct {
	logs []BatchLog
}

func (l *memoryLogger) LogBatch(log BatchLog) {
	l.logs = append(l.logs, log)
}
