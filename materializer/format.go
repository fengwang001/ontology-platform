package materializer

import (
	"fmt"
	"strings"
)

func opName(op Op) string {
	switch op {
	case Put:
		return "PUT"
	case Delete:
		return "DELETE"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", op)
	}
}

func formatExpect(expect *string) string {
	if expect == nil {
		return "<absent>"
	}
	return fmt.Sprintf("%q", *expect)
}

func stagedValue(staged map[string]string, key string) string {
	v, ok := staged[key]
	if !ok {
		return "<absent>"
	}
	return v
}

func formatBatch(batch []Event) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, event := range batch {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "{idx:%d key:%q op:%s value:%q expect:%s}",
			i, event.Key, opName(event.Op), event.Value, formatExpect(event.Expect))
	}
	b.WriteByte(']')
	return b.String()
}

func formatMap(view map[string]string) string {
	if len(view) == 0 {
		return "{}"
	}
	parts := make([]string, 0, len(view))
	for k, v := range view {
		parts = append(parts, fmt.Sprintf("%q=%q", k, v))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}
