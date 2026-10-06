package analyzer

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Logger 记录每次求解的输入、输出与判定依据。
type Logger interface {
	LogEntry(entries []string)
	LogModuleIncluded(id, why string)
	LogDeclKept(module, decl string, reason Reason, because string)
	LogBindError(module, target, imported string, err error)
	LogResult(kept []KeptDecl, included []string)
}

// NopLogger 丢弃日志。
type NopLogger struct{}

func (NopLogger) LogEntry([]string)                          {}
func (NopLogger) LogModuleIncluded(string, string)           {}
func (NopLogger) LogDeclKept(string, string, Reason, string) {}
func (NopLogger) LogBindError(string, string, string, error) {}
func (NopLogger) LogResult([]KeptDecl, []string)             {}

// TextLogger 以可读文本写入 io.Writer。
type TextLogger struct {
	mu sync.Mutex
	w  io.Writer
}

// NewTextLogger 创建文本日志器；w 为 nil 时写入 stderr。
func NewTextLogger(w io.Writer) *TextLogger {
	if w == nil {
		w = os.Stderr
	}
	return &TextLogger{w: w}
}

func (l *TextLogger) line(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.w, "[%s] %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
}

func (l *TextLogger) LogEntry(entries []string) {
	l.line("input entries=%v", entries)
}
func (l *TextLogger) LogModuleIncluded(id, why string) {
	l.line("include module %q (%s)", id, why)
}
func (l *TextLogger) LogDeclKept(module, decl string, reason Reason, because string) {
	l.line("keep %s/%s reason=%s because=%s", module, decl, reason, because)
}
func (l *TextLogger) LogBindError(module, target, imported string, err error) {
	l.line("binding error at module=%q target=%q imported=%q: %v", module, target, imported, err)
}
func (l *TextLogger) LogResult(kept []KeptDecl, included []string) {
	l.line("result included=%v kept=%d", included, len(kept))
}
