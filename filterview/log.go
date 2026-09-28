package filterview

import (
	"fmt"
	"strings"
)

// Logger 接收判定日志：每条输入、每条输出及判定依据。
type Logger interface {
	Printf(format string, args ...any)
}

// SetLogger 安装判定日志输出目标；传 nil 关闭日志。
func (m *Maintainer) SetLogger(l Logger) {
	if l == nil {
		m.logger.Store(nil)
		return
	}
	m.logger.Store(&l)
}

// updateCase 描述更新操作前后取值与过滤区间关系的四种情形。
type updateCase int

const (
	// updateInToInNoop：前后都满足且行不变，无输出。
	updateInToInNoop updateCase = iota + 1
	// updateInToInChanged：前后都满足且行变化，先撤回旧值再写入新值。
	updateInToInChanged
	// updateInToOut：前满足后不满足，撤回旧值。
	updateInToOut
	// updateOutToIn：前不满足后满足，写入新值。
	updateOutToIn
	// updateOutToOut：前后都不满足，无输出。
	updateOutToOut
)

func updateBasis(beforeIn, afterIn, sameRow bool) updateCase {
	switch {
	case beforeIn && afterIn && sameRow:
		return updateInToInNoop
	case beforeIn && afterIn:
		return updateInToInChanged
	case beforeIn:
		return updateInToOut
	case afterIn:
		return updateOutToIn
	default:
		return updateOutToOut
	}
}

func (c updateCase) String() string {
	switch c {
	case updateInToInNoop:
		return "in->in unchanged: no output"
	case updateInToInChanged:
		return "in->in changed: ViewDelete(old) then ViewInsert(new)"
	case updateInToOut:
		return "in->out: ViewDelete(old)"
	case updateOutToIn:
		return "out->in: ViewInsert(new)"
	default:
		return "out->out: no output"
	}
}

func inWord(in bool) string {
	if in {
		return "in [Low, High)"
	}
	return "out of [Low, High)"
}

func formatRow(r Row) string {
	return fmt.Sprintf("{Key:%q Value:%d}", r.Key, r.Value)
}

func formatChanges(changes []Change) string {
	var b strings.Builder
	for i, c := range changes {
		if i > 0 {
			b.WriteString(", ")
		}
		kind := "ViewInsert"
		if c.Kind == ViewDelete {
			kind = "ViewDelete"
		}
		fmt.Fprintf(&b, "%s %s", kind, formatRow(c.Row))
	}
	if b.Len() == 0 {
		return "(none)"
	}
	return b.String()
}

func (m *Maintainer) decideInsert(index int, row Row, afterIn bool) string {
	out := "no output"
	if afterIn {
		out = "ViewInsert " + formatRow(row)
	}
	return fmt.Sprintf("op[%d] INSERT input=%s decide: value %d %s -> %s",
		index, formatRow(row), row.Value, inWord(afterIn), out)
}

func (m *Maintainer) decideDelete(index int, row Row, beforeIn bool) string {
	out := "no output"
	if beforeIn {
		out = "ViewDelete " + formatRow(row)
	}
	return fmt.Sprintf("op[%d] DELETE input=%s decide: value %d %s -> %s",
		index, formatRow(row), row.Value, inWord(beforeIn), out)
}

func (m *Maintainer) decideUpdate(index int, before, after Row, beforeIn, afterIn bool, c updateCase) string {
	return fmt.Sprintf("op[%d] UPDATE input=before %s -> after %s decide: before %s, after %s; %s",
		index, formatRow(before), formatRow(after),
		inWord(beforeIn), inWord(afterIn), c)
}

func (m *Maintainer) emitLogs(seq uint64, entries []string, changes []Change) {
	lp := m.logger.Load()
	if lp == nil {
		return
	}
	l := *lp
	l.Printf("filterview batch #%d begin", seq)
	for _, e := range entries {
		l.Printf("%s", e)
	}
	l.Printf("filterview batch #%d output=%s", seq, formatChanges(changes))
	l.Printf("filterview batch #%d committed", seq)
}
