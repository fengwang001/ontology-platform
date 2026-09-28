package keygroup

import (
	"io"
	"log"
	"os"
)

// stdLogger 把标准库 *log.Logger 适配为组件的 Logger。
type stdLogger struct {
	l *log.Logger
}

func (l stdLogger) Printf(format string, args ...any) {
	l.l.Printf(format, args...)
}

// NewLogger 创建写入 w 的标准日志器；w 为 nil 时写入标准错误。
func NewLogger(w io.Writer) Logger {
	if w == nil {
		w = os.Stderr
	}
	return stdLogger{l: log.New(w, "", log.LstdFlags|log.Lmicroseconds)}
}
