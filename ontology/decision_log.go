package ontology

import (
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"
)

// DecisionLog 记录每次判定（查询 / 重建 / 复核）的输入、输出与依据。
type DecisionLog struct {
	mu sync.Mutex
	w  io.Writer
}

// DecisionRecord 是一条可 JSON 序列化的判定记录。
type DecisionRecord struct {
	Time     string      `json:"time"`
	Kind     string      `json:"kind"`
	Input    interface{} `json:"input"`
	Output   interface{} `json:"output"`
	Basis    string      `json:"basis"`
	Decision string      `json:"decision"`
}

// 判定类别常量。
const (
	KindRebuild = "rebuild_decision"
	KindQuery   = "query_decision"
	KindVerify  = "verify_decision"
	KindWrite   = "write_decision"
)

// NewDecisionLog 创建一个默认写往 stdout 的判定日志。
//
// 日志只服务于“打印每次判定的输入、输出与依据”的可观测要求；
// 复核正确性绝不依赖该日志（复核输入仅为审计记录 + 对象当前状态）。
func NewDecisionLog() *DecisionLog {
	l := &DecisionLog{}
	l.w = os.Stdout
	return l
}

// SetSink 更换日志输出目标（测试中常用 bytes.Buffer 捕获）。
func (l *DecisionLog) SetSink(w io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.w = w
}

// Write 原子地写入一条 JSON 行；Sink 为 nil 时静默丢弃。
func (l *DecisionLog) Write(rec DecisionRecord) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	w := l.w
	l.mu.Unlock()
	if w == nil {
		return nil
	}
	if rec.Time == "" {
		rec.Time = time.Now().UTC().Format(time.RFC3339Nano)
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err = l.w.Write(line)
	return err
}

var _ = os.Stdout
var _ = json.Marshal
