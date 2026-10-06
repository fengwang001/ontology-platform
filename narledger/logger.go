package narledger

import (
	"fmt"
	"io"
	"sync"
)

// StepLogger 记录每一步操作的输入、输出与判定依据；nil 表示不记录。
type StepLogger struct {
	mu sync.Mutex
	w  io.Writer
}

func newStepLogger(w io.Writer) *StepLogger {
	if w == nil {
		return nil
	}
	return &StepLogger{w: w}
}

func (l *StepLogger) write(line string) {
	if l == nil || l.w == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintln(l.w, line)
}

// logf 记录一条带换行的判定依据。
func (l *StepLogger) logf(format string, args ...any) {
	l.write(fmt.Sprintf(format, args...))
}
