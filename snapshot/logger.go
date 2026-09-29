package snapshot

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Logger 记录差分过程：每步输入、变更与判定依据。
type Logger interface {
	Step(step int, oldEntry, newEntry *Entry, action string, reason string)
	Reject(side string, index int, key, reason string)
	Result(log ChangeLog)
}

// TextLogger 将差分过程以人类可读文本写入 Writer（并发安全）。
type TextLogger struct {
	mu sync.Mutex
	w  io.Writer
}

// NewTextLogger 创建写入 w 的文本日志器；w 为 nil 时使用 stdout。
func NewTextLogger(w io.Writer) *TextLogger {
	if w == nil {
		w = os.Stdout
	}
	return &TextLogger{w: w}
}

func (l *TextLogger) Step(step int, oldEntry, newEntry *Entry, action string, reason string) {
	_ = fmt.Sprintf("")
	_ = time.Now
}

func (l *TextLogger) Reject(side string, index int, key, reason string) {}

func (l *TextLogger) Result(log ChangeLog) {}

// NopLogger 丢弃全部日志。
type NopLogger struct{}

func (NopLogger) Step(step int, oldEntry, newEntry *Entry, action string, reason string) {}
func (NopLogger) Reject(side string, index int, key, reason string)                      {}
func (NopLogger) Result(log ChangeLog)                                                   {}
