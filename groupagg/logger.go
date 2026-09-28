package groupagg

import (
	"fmt"
	"io"
	"strings"
)

// Logger 以人类可读的形式打印输入操作、输出条目与判定依据。
// 它只是诊断日志，不构成变更日志；被拒绝的批次不会在变更日志中留痕。
type Logger struct {
	w io.Writer
}

// NewLogger 创建一个写入 w 的诊断日志器；w 为 nil 时所有输出被丢弃。
func NewLogger(w io.Writer) *Logger {
	return &Logger{w: w}
}

func opString(op Op) string {
	switch op.Kind {
	case OpInsert:
		return fmt.Sprintf("INSERT id=%q group=%q value=%d", op.ID, op.GroupKey, op.Value)
	case OpUpdate:
		return fmt.Sprintf("UPDATE id=%q group=%q value=%d", op.ID, op.GroupKey, op.Value)
	case OpDelete:
		return fmt.Sprintf("DELETE id=%q", op.ID)
	default:
		return fmt.Sprintf("UNKNOWN(%d) id=%q group=%q value=%d", op.Kind, op.ID, op.GroupKey, op.Value)
	}
}

func changeString(e ChangeEntry) string {
	verb := "PUT"
	if e.Kind == ChangeRetract {
		verb = "RETRACT"
	}
	return fmt.Sprintf("  seq=%d %-7s group=%-6q sum=%d count=%d (from row %q)",
		e.Seq, verb, e.Group, e.Sum, e.Count, e.RowID)
}

// LogAccepted 打印一个被接受操作的输入、输出条目与判定依据。
func (l *Logger) LogAccepted(report OpReport) {
	if l == nil || l.w == nil {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[ACCEPT] %s\n", opString(report.Op))
	fmt.Fprintf(&b, "  basis: %s\n", report.Basis)
	if len(report.Entries) == 0 {
		b.WriteString("  entries: (none)\n")
	} else {
		fmt.Fprintf(&b, "  entries (%d):\n", len(report.Entries))
		for _, e := range report.Entries {
			b.WriteString(changeString(e))
			b.WriteByte('\n')
		}
	}
	_, _ = io.WriteString(l.w, b.String())
}

// LogRejected 打印一个被拒绝批次的输入操作与拒绝原因。
// 拒绝时整批不产生任何变更，日志明确标注“无副作用”。
func (l *Logger) LogRejected(ops []Op, err *RejectError) {
	if l == nil || l.w == nil {
		return
	}
	var b strings.Builder
	b.WriteString("[REJECT] batch rejected, no state change, no changelog entries\n")
	for i, op := range ops {
		mark := "  "
		if err != nil && i == err.OpIndex {
			mark = ">>"
		}
		fmt.Fprintf(&b, "%s op[%d] %s\n", mark, i, opString(op))
	}
	if err != nil {
		fmt.Fprintf(&b, "  reason: %s — %s\n", err.Reason, err.Message)
	}
	_, _ = io.WriteString(l.w, b.String())
}
