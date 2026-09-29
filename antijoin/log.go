package antijoin

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// Step 记录一次输入的输入、判定依据与输出。
type Step struct {
	Seq      int
	Input    Change
	Accepted bool
	Basis    string
	Outputs  []Output
}

// Logger 接收每一步的输入、输出与判定依据。
type Logger interface {
	Log(Step)
}

// NopLogger 不输出任何内容。
type NopLogger struct{}

// Log 实现 Logger。
func (NopLogger) Log(Step) {}

// PrintLogger 把每一步的输入、输出与判定依据打印到 Writer，可安全并发使用。
type PrintLogger struct {
	mu sync.Mutex
	W  io.Writer
}

// NewPrintLogger 创建向 w 输出的打印日志器，w 为 nil 时使用标准输出。
func NewPrintLogger(w io.Writer) *PrintLogger {
	if w == nil {
		w = os.Stdout
	}
	return &PrintLogger{W: w}
}

// Log 实现 Logger。
func (l *PrintLogger) Log(s Step) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if s.Accepted {
		fmt.Fprintf(l.W, "step %d | INPUT  %s | DECISION accepted: %s\n", s.Seq, s.Input, s.Basis)
	} else {
		fmt.Fprintf(l.W, "step   | INPUT  %s | DECISION REJECTED: %s\n", s.Input, s.Basis)
	}
	if len(s.Outputs) == 0 {
		fmt.Fprintln(l.W, "         OUTPUT (none)")
	}
	for _, o := range s.Outputs {
		fmt.Fprintf(l.W, "         OUTPUT %s | %s\n", o, o.Reason)
	}
}

// CollectLogger 收集全部步骤（含被拒绝尝试），供测试断言，可安全并发使用。
type CollectLogger struct {
	mu    sync.Mutex
	Steps []Step
}

// Log 实现 Logger。
func (l *CollectLogger) Log(s Step) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Steps = append(l.Steps, cloneStep(s))
}

// Snapshot 返回已记录步骤的副本。
func (l *CollectLogger) Snapshot() []Step {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Step, len(l.Steps))
	copy(out, l.Steps)
	return out
}

func cloneStep(s Step) Step {
	if s.Outputs != nil {
		s.Outputs = append([]Output(nil), s.Outputs...)
	}
	return s
}
