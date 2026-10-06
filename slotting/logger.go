package slotting

import (
	"fmt"
	"io"
	"time"
)

// opLogger 打印每个操作的输入、输出与判定依据。nil io.Writer 表示不打印。
type opLogger struct {
	w io.Writer
}

func newLogger(w io.Writer) *opLogger {
	if w == nil {
		return &opLogger{}
	}
	return &opLogger{w: w}
}

func (l *opLogger) emitf(format string, args ...any) {
	if l == nil || l.w == nil {
		return
	}
	fmt.Fprintf(l.w, "%s %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
}
