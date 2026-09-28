package ontology

import (
	"strconv"
	"strings"
)

// formatQuote 将字符串渲染为带引号的形式，让空串表现为 ""，
// 从而在日志中明确区分“空串值”与“缺列”。
func formatQuote(s string) string {
	return strconv.Quote(s)
}

// formatColumns 以 id=name(default) 的形式渲染列列表。
func formatColumns(cols []ColumnDef) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, c := range cols {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("#")
		b.WriteString(strconv.FormatUint(uint64(c.ID), 10))
		b.WriteByte('=')
		b.WriteString(c.Name)
		b.WriteString("(default=")
		b.WriteString(formatQuote(c.Default))
		b.WriteByte(')')
	}
	b.WriteByte(']')
	return b.String()
}

// formatChanges 渲染一批演进输入。
func formatChanges(changes []Change) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, ch := range changes {
		if i > 0 {
			b.WriteString(", ")
		}
		switch ch.Kind {
		case ChangeAdd:
			b.WriteString("add(name=")
			b.WriteString(formatQuote(ch.Name))
			b.WriteString(",default=")
			b.WriteString(formatQuote(ch.Default))
			b.WriteByte(')')
		case ChangeDrop:
			b.WriteString("drop(id=#")
			b.WriteString(strconv.FormatUint(uint64(ch.ID), 10))
			b.WriteByte(')')
		case ChangeRename:
			b.WriteString("rename(id=#")
			b.WriteString(strconv.FormatUint(uint64(ch.ID), 10))
			b.WriteString(",name=")
			b.WriteString(formatQuote(ch.Name))
			b.WriteByte(')')
		default:
			b.WriteString("unknown(kind=")
			b.WriteString(strconv.Itoa(int(ch.Kind)))
			b.WriteByte(')')
		}
	}
	b.WriteByte(']')
	return b.String()
}

// formatEvent 渲染解码输入：版本号与逐位置的值（保留空串）。
func formatEvent(e Event) string {
	var b strings.Builder
	b.WriteString("{version=")
	b.WriteString(strconv.Itoa(e.Version))
	b.WriteString(",values=[")
	for i, v := range e.Values {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(formatQuote(v))
	}
	b.WriteString("]}")
	return b.String()
}

// sourceName 渲染取值来源。
func sourceName(s ValueSource) string {
	switch s {
	case SourceEvent:
		return "event"
	case SourceDefault:
		return "current-default"
	default:
		return "unknown"
	}
}

// formatRow 渲染解码结果：id=name=value(source)。
func formatRow(r Row) string {
	cvs := r.Ordered()
	var b strings.Builder
	b.WriteByte('[')
	for i, cv := range cvs {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("#")
		b.WriteString(strconv.FormatUint(uint64(cv.Column.ID), 10))
		b.WriteByte('=')
		b.WriteString(cv.Column.Name)
		b.WriteByte('=')
		b.WriteString(formatQuote(cv.Value))
		b.WriteString("(from=")
		b.WriteString(sourceName(cv.Source))
		b.WriteByte(')')
	}
	b.WriteByte(']')
	return b.String()
}

// formatRows 渲染多行结果。
func formatRows(rows []Row) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, r := range rows {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(formatRow(r))
	}
	b.WriteByte(']')
	return b.String()
}

// formatBasis 渲染判定依据：逐列说明按哪个标识、来自事件何位置或当前默认值。
func formatBasis(r Row) string {
	cvs := r.Ordered()
	var b strings.Builder
	b.WriteString("by-stable-id{")
	for i, cv := range cvs {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("#")
		b.WriteString(strconv.FormatUint(uint64(cv.Column.ID), 10))
		b.WriteByte(':')
		b.WriteString(sourceName(cv.Source))
	}
	b.WriteByte('}')
	return b.String()
}
