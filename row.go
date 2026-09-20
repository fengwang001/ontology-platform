package ontology

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Row is a single table row: a set of named attributes.
type Row = map[string]any

// deepCopyValue returns a copy of v that shares no mutable state with it.
// Maps and slices are copied recursively; scalars are copied as-is.
func deepCopyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = deepCopyValue(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = deepCopyValue(val)
		}
		return out
	default:
		return v
	}
}

// deepCopyRow returns a fresh Row whose values are deep copies of r's.
func deepCopyRow(r Row) Row {
	out := make(Row, len(r))
	for k, v := range r {
		out[k] = deepCopyValue(v)
	}
	return out
}

// canonicalRow renders a row as a deterministic string: attributes sorted
// by name, each rendered as "name=type:value". It defines the row identity
// used for input-order-independent tie-breaking (see package doc).
func canonicalRow(r Row) string {
	names := make([]string, 0, len(r))
	for name := range r {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteByte('{')
	for i, name := range names {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Quote(name))
		b.WriteByte('=')
		b.WriteString(canonicalValue(r[name]))
	}
	b.WriteByte('}')
	return b.String()
}

// canonicalValue renders a single value with an explicit type tag so that,
// e.g., the string "1" and the number 1 never collide.
func canonicalValue(v any) string {
	switch t := v.(type) {
	case nil:
		return "nil"
	case string:
		return "s:" + strconv.Quote(t)
	case bool:
		return "b:" + strconv.FormatBool(t)
	case int:
		return "n:" + strconv.Itoa(t)
	case int8:
		return "n:" + strconv.FormatInt(int64(t), 10)
	case int16:
		return "n:" + strconv.FormatInt(int64(t), 10)
	case int32:
		return "n:" + strconv.FormatInt(int64(t), 10)
	case int64:
		return "n:" + strconv.FormatInt(t, 10)
	case uint:
		return "n:" + strconv.FormatUint(uint64(t), 10)
	case uint8:
		return "n:" + strconv.FormatUint(uint64(t), 10)
	case uint16:
		return "n:" + strconv.FormatUint(uint64(t), 10)
	case uint32:
		return "n:" + strconv.FormatUint(uint64(t), 10)
	case uint64:
		return "n:" + strconv.FormatUint(t, 10)
	case float32:
		return "f:" + canonicalFloat(float64(t))
	case float64:
		return "f:" + canonicalFloat(t)
	case map[string]any:
		return "m:" + canonicalRow(t)
	case []any:
		parts := make([]string, len(t))
		for i, val := range t {
			parts[i] = canonicalValue(val)
		}
		return "a:[" + strings.Join(parts, ",") + "]"
	default:
		return fmt.Sprintf("%T:%v", v, v)
	}
}

// canonicalFloat renders a float deterministically, normalizing -0.0 to 0.0
// so that the two zeros share one identity.
func canonicalFloat(f float64) string {
	if f == 0 {
		f = 0
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}
