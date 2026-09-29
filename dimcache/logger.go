package dimcache

import (
	"fmt"
	"io"
	"sync"
)

// Logger 记录每一步的输入、栅栏、缓存与判定依据。
type Logger interface {
	Logf(format string, args ...any)
}

// NewLogger 创建一个带互斥保护、可被并发安全使用的文本日志器。
// w 为 nil 时返回一个丢弃输出的日志器。
func NewLogger(w io.Writer) Logger {
	return &textLogger{w: w}
}

type textLogger struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *textLogger) Logf(format string, args ...any) {
	if l == nil || l.w == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.w, format+"\n", args...)
}

type discardLogger struct{}

func (discardLogger) Logf(string, ...any) {}
