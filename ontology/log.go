package ontology

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// JudgeLogger 输出逐条事件的判定日志：输入、判定结果与依据。
type JudgeLogger interface {
	// Log 记录一条事件判定。
	Log(entry JudgeEntry)
}

// JudgeEntry 是一条判定日志。
type JudgeEntry struct {
	Seq    int64
	Key    string
	Op     Op
	Input  Event
	Result string
	Basis  string
}

// TextJudgeLogger 把判定日志以“输入 -> 判定结果（依据）”的单行格式
// 写入给定 io.Writer，便于测试与人工核查。
type TextJudgeLogger struct {
	w io.Writer
}

// NewTextJudgeLogger 创建一个写入 w 的文本判定日志器。
func NewTextJudgeLogger(w io.Writer) *TextJudgeLogger {
	return &TextJudgeLogger{w: w}
}

// Log 实现 JudgeLogger。
func (l *TextJudgeLogger) Log(e JudgeEntry) {
	fmt.Fprintf(l.w, "seq=%d key=%q op=%s input={before=%s after=%s} => %s（依据: %s）\n",
		e.Seq, e.Key, e.Op, formatRow(e.Input.Before), formatRow(e.Input.After), e.Result, e.Basis)
}

// rowsEqual 按“列名与值的集合”比较两行镜像是否完全一致：
// 键集合必须相同，且每个共有键的值相等。
// 缺列（键不存在）与空串（键存在但值为 ""）因此不相等。
func rowsEqual(a, b Row) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok || vb != va {
			return false
		}
	}
	return true
}

// formatRow 以稳定的列顺序渲染行镜像；nil 渲染为 <absent>，
// 空串值显式渲染为 ""，与缺列形成可区分的日志。
func formatRow(r Row) string {
	if r == nil {
		return "<absent>"
	}
	keys := make([]string, 0, len(r))
	for k := range r {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%q", k, r[k]))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}
