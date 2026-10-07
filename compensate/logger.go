package compensate

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// AttemptEvent 记录一次“尝试”（生效 / 补偿 / 直接补偿请求）的输入、结果与判定依据。
type AttemptEvent struct {
	Time     time.Time
	Phase    string // "apply" | "compensate" | "direct_compensate_request"
	ActionID string
	Branch   string
	OpIndex  int
	OpID     string
	Input    string // 尝试的输入描述
	Result   string // 结果描述
	Reason   string // 判定依据
	Allowed  bool   // 该尝试是否被允许执行
}

// Logger 接收每次补偿尝试的审计日志。
type Logger interface {
	LogAttempt(ev AttemptEvent)
}

// TextLogger 以每行一条的方式输出尝试日志，并发安全。
type TextLogger struct {
	mu sync.Mutex
	w  io.Writer
}

func NewTextLogger(w io.Writer) *TextLogger { return &TextLogger{w: w} }

func (l *TextLogger) LogAttempt(ev AttemptEvent) {
	idx := "-"
	if ev.OpIndex >= 0 {
		idx = fmt.Sprintf("%d", ev.OpIndex)
	}
	verdict := "ALLOWED"
	if !ev.Allowed {
		verdict = "REJECTED"
	}
	op := ev.OpID
	if op == "" {
		op = "-"
	}
	line := fmt.Sprintf("%s | phase=%s | %s/%s#%s op=%s | %s | input={%s} | result={%s} | reason={%s}",
		ev.Time.Format("15:04:05.000000"),
		ev.Phase, ev.ActionID, ev.Branch, idx, op,
		verdict, ev.Input, ev.Result, ev.Reason)
	l.write(line)
}

func (l *TextLogger) write(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintln(l.w, line)
}

// MultiLogger 把事件扇出到多个 logger。
type MultiLogger []Logger

func (m MultiLogger) LogAttempt(ev AttemptEvent) {
	for _, lg := range m {
		lg.LogAttempt(ev)
	}
}

var _ Logger = (*TextLogger)(nil)
var _ Logger = (MultiLogger)(nil)
