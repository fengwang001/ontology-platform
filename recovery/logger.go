package recovery

import (
	"fmt"
	"io"
	"sync"
)

// NopLogger 丢弃全部判定日志（默认值）。
type NopLogger struct{}

// Log 实现 DecisionLogger。
func (NopLogger) Log(DecisionLogEntry) {}

// TextLogger 以“输入/输出/依据”三要素文本格式写出每次判定。
// 并发安全，可直接挂在 os.Stdout / 测试缓冲区上。
type TextLogger struct {
	mu sync.Mutex
	w  io.Writer
}

// NewTextLogger 构造文本判定日志器。
func NewTextLogger(w io.Writer) *TextLogger {
	return &TextLogger{w: w}
}

// Log 实现 DecisionLogger。
func (l *TextLogger) Log(e DecisionLogEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.w, "[decision] stage=%s\n  input : %s\n  output: %s\n  reason: %s\n",
		e.Stage, e.Input, e.Output, e.Reason)
}
