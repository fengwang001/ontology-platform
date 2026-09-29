package ontology

import (
	"strconv"
	"strings"
)

// String returns the human-readable name of an operation.
func (o Op) String() string {
	switch o {
	case Write:
		return "write"
	case Delete:
		return "delete"
	default:
		return "invalid"
	}
}

func formatExpect(v *string) string {
	if v == nil {
		return "<absent>"
	}
	return strconv.Quote(*v)
}

func formatBatch(batch []Event) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, e := range batch {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('{')
		b.WriteString(e.Op.String())
		b.WriteByte(' ')
		b.WriteString(strconv.Quote(e.Key))
		if e.Op == Write {
			b.WriteString(" value=")
			b.WriteString(strconv.Quote(e.Value))
		}
		b.WriteString(" expect=")
		b.WriteString(formatExpect(e.Expect))
		b.WriteByte('}')
	}
	b.WriteByte(']')
	return b.String()
}
