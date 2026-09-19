package aggregate

import (
	"fmt"
	"reflect"
	"strconv"
)

type partKind int

const (
	partPresent partKind = iota
	partEmpty
	partNull
	partAbsent
)

// partID is the canonical, collision-free identity of one key column.
// Present values are rendered through a "type|value" encoding so that, for
// example, the integer 1 and the string "1" can never collapse together.
type partID struct {
	kind partKind
	repr string
}

// groupID is the identity of a composite group, one part per key column.
// key is a length-prefixed concatenation of column identities, which is
// comparable and usable as a Go map key; parts is retained for ordering and
// rebuilding caller-facing GroupKeys.
type groupID struct {
	key   string
	parts []partID
}

func makeGroupID(parts []partID) groupID {
	var b []byte
	for _, p := range parts {
		b = strconv.AppendInt(b, int64(p.kind), 10)
		b = append(b, ':')
		b = strconv.AppendInt(b, int64(len(p.repr)), 10)
		b = append(b, ':')
		b = append(b, p.repr...)
		b = append(b, ';')
	}
	return groupID{key: string(b), parts: parts}
}

func classifyPart(name string, row map[string]any) partID {
	value, ok := row[name]
	if !ok {
		return partID{kind: partAbsent}
	}
	if value == nil {
		return partID{kind: partNull}
	}
	if s, ok := value.(string); ok && s == "" {
		return partID{kind: partEmpty}
	}
	return partID{kind: partPresent, repr: encodeValue(value)}
}

// encodeValue renders any present grouping value as a stable, unambiguous
// string. Non-comparable dynamic types (maps, slices) that cannot be Go map
// keys are rendered with fmt's #v verb instead of being compared directly.
func encodeValue(value any) string {
	if reflect.TypeOf(value).Comparable() {
		return fmt.Sprintf("%T|%v", value, value)
	}
	return fmt.Sprintf("%T|%#v", value, value)
}

func idToPart(name string, id partID) KeyPart {
	part := KeyPart{Name: name}
	switch id.kind {
	case partAbsent:
		part.Kind = Absent
		part.Missing = Absent
	case partNull:
		part.Kind = Null
		part.Missing = Null
	case partEmpty:
		part.Kind = EmptyString
		part.Missing = EmptyString
		part.Value = ""
	default:
		part.Kind = Present
		part.Value = decodePresent(id.repr)
	}
	return part
}

// decodePresent is best effort: the canonical identity is repr itself.
// Standard grouping types come back with their original Go type for
// caller convenience.
func decodePresent(repr string) any {
	for _, typ := range []string{"string", "int64", "float64", "bool"} {
		prefix := typ + "|"
		if len(repr) > len(prefix) && repr[:len(prefix)] == prefix {
			body := repr[len(prefix):]
			switch typ {
			case "string":
				return body
			case "int64":
				if n, err := strconv.ParseInt(body, 10, 64); err == nil {
					return n
				}
			case "float64":
				if f, err := strconv.ParseFloat(body, 64); err == nil {
					return f
				}
			case "bool":
				return body == "true"
			}
		}
	}
	return repr
}

// comparePartID implements the fixed column ordering: present values first
// (ascending by canonical encoding), then empty string, then null, then
// absent. Missing groups therefore occupy consistent output positions
// regardless of arrival order.
func comparePartID(a, b partID) int {
	rank := func(k partKind) int {
		switch k {
		case partPresent:
			return 0
		case partEmpty:
			return 1
		case partNull:
			return 2
		default:
			return 3
		}
	}
	ra, rb := rank(a.kind), rank(b.kind)
	if ra != rb {
		if ra < rb {
			return -1
		}
		return 1
	}
	if a.repr < b.repr {
		return -1
	}
	if a.repr > b.repr {
		return 1
	}
	return 0
}

func compareParts(a, b []partID) int {
	for i := range a {
		if c := comparePartID(a[i], b[i]); c != 0 {
			return c
		}
	}
	return 0
}
