package ontology

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

// DecisionLogEntry 记录每次判定的输入、输出与依据。
type DecisionLogEntry struct {
	Time  string      `json:"time"`
	Op    string      `json:"op"`
	Input interface{} `json:"input"`
	Basis string      `json:"basis"`
	Out   interface{} `json:"out"`
}

// DecisionLogger 接收判定日志（写操作、查询、重建、复核均记录）。
type DecisionLogger interface {
	Log(e DecisionLogEntry)
}

// JSONLogger 将判定日志以一行一条 JSON 写入 w，并发安全。
type JSONLogger struct {
	mu sync.Mutex
	w  io.Writer
}

func NewJSONLogger(w io.Writer) *JSONLogger { return &JSONLogger{w: w} }

func (l *JSONLogger) Log(e DecisionLogEntry) {
	if e.Time == "" {
		e.Time = time.Now().UTC().Format(time.RFC3339Nano)
	}
	data, _ := json.Marshal(e)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.w.Write(append(data, '\n'))
}
