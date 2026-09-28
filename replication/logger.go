package replication

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// Verdict 是单条事件的判定结果。
type Verdict string

const (
	// VerdictApplied 事件已成功应用。
	VerdictApplied Verdict = "APPLIED"
	// VerdictConflict 事件被判定为冲突并跳过，副本未被该事件改变。
	VerdictConflict Verdict = "CONFLICT"
)

// Decision 是一条事件的判定日志记录：输入、判定结果与依据。
type Decision struct {
	Seq    int64
	Op     Op
	Key    string
	Before Row
	After  Row
	// Verdict 为 APPLIED 或 CONFLICT。整批被拒绝时不产生 Decision，只产生拒绝日志行。
	Verdict Verdict
	// Basis 是判定依据的人类可读说明。
	Basis string
	// Conflict 判定为冲突时的冲突明细；否则为 nil。
	Conflict *Conflict
}

// DecisionLogger 记录逐条判定与整批拒绝。
// 实现必须保证输出确定性：同一输入序列产生逐字节相同的输出。
type DecisionLogger interface {
	LogDecision(Decision)
	LogRejection(reason RejectReason, detail string)
}

// nopLogger 丢弃所有日志。
type nopLogger struct{}

func (nopLogger) LogDecision(Decision)              {}
func (nopLogger) LogRejection(RejectReason, string) {}

// NewNopLogger 返回丢弃所有输出的判定日志器。
func NewNopLogger() DecisionLogger { return nopLogger{} }

// TextLogger 以稳定的单行文本格式记录判定。并发安全。
type TextLogger struct {
	mu sync.Mutex
	w  io.Writer
}

// NewTextLogger 返回把判定以稳定文本格式写入 w 的日志器。
func NewTextLogger(w io.Writer) *TextLogger {
	return &TextLogger{w: w}
}

// LogDecision 记录一条判定。行镜像以 JSON 渲染：
// nil 镜像输出 null（行不存在），非 nil 空镜像输出 {}，缺列直接不出现——
// 与值为空串 "" 的列可区分；map 的 JSON 键顺序固定，输出确定。
func (l *TextLogger) LogDecision(d Decision) {
	l.mu.Lock()
	defer l.mu.Unlock()
	line := fmt.Sprintf("[decision] seq=%d op=%s key=%s before=%s after=%s -> %s",
		d.Seq, d.Op, jsonToken(d.Key), rowJSON(d.Before), rowJSON(d.After), d.Verdict)
	if d.Conflict != nil {
		line += " " + string(d.Conflict.Kind)
	}
	line += ": " + d.Basis
	fmt.Fprintln(l.w, line)
}

// LogRejection 记录整批拒绝。
func (l *TextLogger) LogRejection(reason RejectReason, detail string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.w, "[rejection] reason=%s: %s\n", reason, detail)
}

// rowJSON 以确定性 JSON 渲染行镜像。
func rowJSON(r Row) string {
	if r == nil {
		return "null"
	}
	b, err := json.Marshal(r)
	if err != nil {
		// map[string]string 理论上不会序列化失败，退回确定性的占位输出。
		return `"<unrenderable>"`
	}
	return string(b)
}

// jsonToken 以 JSON 字符串字面量渲染普通字符串。
func jsonToken(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
