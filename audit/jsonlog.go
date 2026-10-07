package audit

import (
	"encoding/json"
	"io"
	"sync"
)

// JSONLLogger 以 JSON Lines 形式写出完整调用日志，
// 每行包含一次调用的输入、最终输出与裁决依据。
type JSONLLogger struct {
	mu  sync.Mutex
	w   io.Writer
	err error
}

func NewJSONLLogger(w io.Writer) *JSONLLogger { return &JSONLLogger{w: w} }

func (l *JSONLLogger) LogEvent(e Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return
	}
	b, err := json.Marshal(e)
	if err != nil {
		l.err = err
		return
	}
	if _, err := l.w.Write(append(b, '\n')); err != nil {
		l.err = err
	}
}

// Err 返回写入过程中出现的首个错误。
func (l *JSONLLogger) Err() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.err
}
