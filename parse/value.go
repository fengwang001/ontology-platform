package parse

import (
	"sort"
	"strconv"
	"strings"
)

// Kind identifies the seven JSON value types; true and false are distinct.
type Kind int

const (
	KindNull Kind = iota
	KindTrue
	KindFalse
	KindNumber
	KindString
	KindArray
	KindObject
)

// Value is the in-memory representation of one JSON value.
type Value struct {
	Kind Kind
	Num  float64
	Str  string
	Arr  []Value
	Obj  map[string]Value
}

// String serializes to deterministic JSON text (object keys sorted).
func (v Value) String() string {
	switch v.Kind {
	case KindNull:
		return "null"
	case KindTrue:
		return "true"
	case KindFalse:
		return "false"
	case KindNumber:
		return strconv.FormatFloat(v.Num, 'g', -1, 64)
	case KindString:
		return enc(v.Str)
	case KindArray:
		parts := make([]string, len(v.Arr))
		for i, e := range v.Arr {
			parts[i] = e.String()
		}
		return "[" + strings.Join(parts, ",") + "]"
	case KindObject:
		keys := make([]string, 0, len(v.Obj))
		for k := range v.Obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			keys[i] = enc(k) + ":" + v.Obj[k].String()
		}
		return "{" + strings.Join(keys, ",") + "}"
	}
	return ""
}

var esc = map[rune]string{'"': `\"`, '\\': `\\`, '\n': `\n`, '\r': `\r`, '\t': `\t`, '\b': `\b`, '\f': `\f`}

var isSpace = [256]bool{'\t': true, '\n': true, '\r': true, ' ': true}
var isNumTok = [256]bool{'.': true, '-': true, '+': true, 'e': true, 'E': true, '0': true, '1': true, '2': true, '3': true, '4': true, '5': true, '6': true, '7': true, '8': true, '9': true}

func enc(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		if e, ok := esc[r]; ok {
			b.WriteString(e)
		} else if r < 0x20 {
			b.WriteString(`\u00`)
			b.WriteByte("0123456789abcdef"[r>>4])
			b.WriteByte("0123456789abcdef"[r&15])
		} else {
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
