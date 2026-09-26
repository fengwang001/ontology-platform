package parse

import (
	"errors"
	"sort"
	"strconv"
	"strings"

	"ontology/lex"
)

type Kind int

const (
	Null Kind = iota
	Bool
	Num
	Str
	Arr
	Obj
)

type Value struct { // Bool 用 Num 0/1 表示
	Kind Kind
	Num  float64
	Str  string
	Arr  []Value
	Obj  map[string]Value
}

var ErrDupKey, ErrSyntax = errors.New("parse: duplicate key"), errors.New("parse: invalid syntax")

func fnv(s string) uint64 {
	h := uint64(14695981039346656037)
	for j := 0; j < len(s); j++ {
		h = (h ^ uint64(s[j])) * 1099511628211
	}
	return h
}

func (p *parser) num() (Value, error) {
	st := p.i
	for p.i < len(p.b) && strings.IndexByte("-+.eE0123456789", p.b[p.i]) >= 0 {
		p.i++
	}
	n, err := lex.ParseNumber(string(p.b[st:p.i]))
	if err != nil {
		return Value{}, err
	}
	return Value{Kind: Num, Num: n}, nil
}

func (p *parser) str() (string, error) {
	p.i++
	st := p.i
	for p.i < len(p.b) {
		switch p.b[p.i] {
		case '\\':
			p.i += 2
		case '"':
			s, err := lex.ParseString(p.b[st:p.i])
			p.i++
			return s, err
		default:
			p.i++
		}
	}
	return "", ErrSyntax
}

func (v Value) String() string { // 键排序，输出确定
	var sb strings.Builder
	v.write(&sb)
	return sb.String()
}

func (v Value) write(sb *strings.Builder) {
	switch v.Kind {
	case Null:
		sb.WriteString("null")
	case Bool:
		w := "false"
		if v.Num != 0 {
			w = "true"
		}
		sb.WriteString(w)
	case Num:
		sb.WriteString(strconv.FormatFloat(v.Num, 'g', -1, 64))
	case Str:
		quote(sb, v.Str)
	case Arr:
		sb.WriteByte('[')
		for i, e := range v.Arr {
			if i > 0 {
				sb.WriteByte(',')
			}
			e.write(sb)
		}
		sb.WriteByte(']')
	case Obj:
		ks := make([]string, 0, len(v.Obj))
		for k := range v.Obj {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		sb.WriteByte('{')
		for i, k := range ks {
			if i > 0 {
				sb.WriteByte(',')
			}
			quote(sb, k)
			sb.WriteByte(':')
			v.Obj[k].write(sb)
		}
		sb.WriteByte('}')
	}
}

func quote(sb *strings.Builder, s string) {
	const hexd = "0123456789abcdef"
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			sb.WriteByte('\\')
			sb.WriteRune(r)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		case '\b':
			sb.WriteString(`\b`)
		case '\f':
			sb.WriteString(`\f`)
		default:
			if r < 0x20 {
				sb.WriteString(`\u00`)
				sb.WriteByte(hexd[r>>4])
				sb.WriteByte(hexd[r&15])
			} else {
				sb.WriteRune(r)
			}
		}
	}
	sb.WriteByte('"')
}
