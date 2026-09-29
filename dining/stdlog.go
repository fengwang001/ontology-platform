package dining

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// StdLogger 以可读文本打印每条判定日志：输入、输出与判定依据。
type StdLogger struct {
	mu sync.Mutex
	w  io.Writer
}

// NewStdLogger 创建输出到 w 的日志器；w 为 nil 时使用标准输出。
func NewStdLogger(w io.Writer) *StdLogger {
	if w == nil {
		w = os.Stdout
	}
	return &StdLogger{w: w}
}

func (l *StdLogger) Log(e Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	verdict := "ACCEPT"
	if !e.Accepted {
		verdict = "REJECT"
	}
	fmt.Fprintf(l.w, "[%04d] %-16s | %s | in: %s | out: %s | because: %s\n",
		e.Seq, e.Op, verdict, e.Input, e.Output, e.Reason)
}
