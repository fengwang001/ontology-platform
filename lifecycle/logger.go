package lifecycle

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// DecisionLog 是一次迁移判定的结构化日志。
type DecisionLog struct {
	Time      time.Time
	BatchSeq  int64
	OpIndex   int
	Input     Op
	Basis     []string
	Fired     []FiredStep
	Committed bool
	Err       *Error
}

// Logger 接收每次迁移的输入、判定依据与最终结果。
type Logger interface {
	LogDecision(DecisionLog)
}

// DiscardLogger 丢弃全部日志。
type DiscardLogger struct{}

// LogDecision 实现 Logger。
func (DiscardLogger) LogDecision(DecisionLog) {}

// TextLogger 以人类可读文本写入 io.Writer。
type TextLogger struct {
	mu sync.Mutex
	w  io.Writer
}

// NewTextLogger 创建文本日志器（默认 stderr）。
func NewTextLogger(w io.Writer) *TextLogger {
	if w == nil {
		w = os.Stderr
	}
	return &TextLogger{w: w}
}

// LogDecision 实现 Logger。
func (l *TextLogger) LogDecision(d DecisionLog) {
	l.mu.Lock()
	defer l.mu.Unlock()
	status := "COMMIT"
	if !d.Committed {
		status = "REJECT"
	}
	fmt.Fprintf(l.w, "%s batch=%d op=%d input=%s -> %s\n",
		d.Time.Format(time.RFC3339Nano), d.BatchSeq, d.OpIndex, formatOp(d.Input), status)
	for _, b := range d.Basis {
		fmt.Fprintf(l.w, "    basis: %s\n", b)
	}
	if d.Err != nil {
		fmt.Fprintf(l.w, "    error: [%s] %s\n", d.Err.Code, d.Err.Detail)
	}
	for _, f := range d.Fired {
		fmt.Fprintf(l.w, "    fired: %s %s:%s->%s (%s)\n",
			f.InstanceID, f.Type, f.From, f.To, f.Transition)
	}
}

func formatOp(o Op) string {
	switch o.Kind {
	case OpFire:
		return fmt.Sprintf("fire(%s,%s)", o.InstanceID, o.Transition)
	case OpSetAttr:
		return fmt.Sprintf("setAttr(%s,%s=%v)", o.InstanceID, o.Attr, o.Value)
	case OpAddLink:
		return fmt.Sprintf("addLink(%s,%s->%s)", o.Link.Type, o.Link.FromID, o.Link.ToID)
	case OpDelLink:
		return fmt.Sprintf("delLink(%s,%s->%s)", o.Link.Type, o.Link.FromID, o.Link.ToID)
	default:
		return "unknown-op"
	}
}
