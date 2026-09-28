package firstrow

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// Row 表示某个键当前的首条存活行。
type Row struct {
	Key       string
	ID        string
	EventTime int64
}

// LogEntry 记录对一条变更的判定依据与产生的输出。
type LogEntry struct {
	Seq      int
	Change   Change
	Accepted bool
	// Reason 仅在 Accepted=false 时有意义。
	Reason   RejectReason
	OldFirst *Row
	NewFirst *Row
	Emitted  []Output
}

// DecisionLogger 记录并可选打印每个输入条目、输出条目与判定依据。
type DecisionLogger struct {
	w   io.Writer
	seq int
	buf []LogEntry
}

// NewLogger 创建判定日志器；w 为 nil 时仅记录不打印。
func NewLogger(w io.Writer) *DecisionLogger {
	if w == nil {
		w = io.Discard
	}
	return &DecisionLogger{w: w}
}

// DefaultLogger 写入标准输出，便于本地运行时观察输入、输出与判定依据。
func DefaultLogger() *DecisionLogger {
	return NewLogger(os.Stdout)
}

// entries 返回到目前为止日志的一份副本。
func (l *DecisionLogger) entries() []LogEntry {
	if l.buf == nil {
		return nil
	}
	out := make([]LogEntry, len(l.buf))
	copy(out, l.buf)
	return out
}

// append 记录一条判定并打印输入、输出与判定依据。
func (l *DecisionLogger) append(e LogEntry) LogEntry {
	l.seq++
	e.Seq = l.seq
	l.buf = append(l.buf, e)

	var b strings.Builder
	fmt.Fprintf(&b, "#%d IN  %s key=%q id=%q eventTime=%d => ", e.Seq, e.Change.Op, e.Change.Key, e.Change.ID, e.Change.EventTime)
	if !e.Accepted {
		fmt.Fprintf(&b, "REJECT %s; live rows and emitted log unchanged", e.Reason)
		fmt.Fprintln(l.w, b.String())
		return e
	}
	fmt.Fprintf(&b, "ACCEPT first(before=%s) first(after=%s)", formatRow(e.OldFirst), formatRow(e.NewFirst))
	if len(e.Emitted) == 0 {
		b.WriteString(" OUT [] (first unchanged)")
	} else {
		b.WriteString(" OUT [")
		for i, o := range e.Emitted {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%s key=%q id=%q eventTime=%d", o.Kind, o.Key, o.ID, o.EventTime)
		}
		b.WriteString("]")
	}
	fmt.Fprintln(l.w, b.String())
	return e
}

// commit 在一批变更全部成功后，按顺序记录并打印每条判定。
// 调用方必须保证整批已成功；被拒绝的批不会调用本方法。
func (l *DecisionLogger) commit(entries []LogEntry) {
	for _, e := range entries {
		l.append(e)
	}
}

func formatRow(r *Row) string {
	if r == nil {
		return "<none>"
	}
	return fmt.Sprintf("%s@%d", r.ID, r.EventTime)
}
