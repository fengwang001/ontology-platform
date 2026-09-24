package mtype

import (
	"errors"
	"sort"
	"strings"
)

// Param 是媒体类型参数；Params 按名排序，名小写、值保持大小写。
type Param struct{ Name, Value string }
type MediaType struct {
	Type, Subtype string
	Params        []Param
}

// 五类可判定解析错误哨兵（ErrBadQ 由 rank 包产生）。
var (
	ErrNoSlash       = errors.New("mtype: missing '/'")
	ErrEmptySubtype  = errors.New("mtype: empty type/subtype")
	ErrParamNoEquals = errors.New("mtype: parameter missing '='")
	ErrUnclosedQuote = errors.New("mtype: unclosed quoted value")
	ErrBadQ          = errors.New("mtype: invalid q")
)

// ParseError 包装哨兵错误并带出 Accept 项序号（0 基）。
type ParseError struct {
	Index int
	Err   error
}

func (e *ParseError) Error() string { return e.Err.Error() }
func (e *ParseError) Unwrap() error { return e.Err }
func (e *ParseError) Is(t error) bool {
	p, ok := t.(*ParseError)
	return ok && e.Index == p.Index && e.Err == p.Err
}

// WithIndex 把解析错误重写为指定 Accept 项序号。
func WithIndex(err error, idx int) error {
	if pe, ok := err.(*ParseError); ok {
		return &ParseError{idx, pe.Err}
	}
	return err
}

// splitQuoted 在引号外按 sep 切分；引号内 sep 与被转义引号不切分。
func splitQuoted(s string, sep byte) []string {
	out, start, inQ, esc := []string{}, 0, false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inQ && esc {
			esc = false
		} else if inQ && c == '\\' {
			esc = true
		} else if inQ && c == '"' {
			inQ = false
		} else if !inQ && c == '"' {
			inQ = true
		} else if !inQ && c == sep {
			out, start = append(out, s[start:i]), i+1
		}
	}
	return append(out, s[start:])
}

// SplitList 按引号外逗号切分 Accept 头部；空白串返回 nil。
func SplitList(h string) []string {
	if strings.TrimSpace(h) == "" {
		return nil
	}
	return splitQuoted(h, ',')
}

// unquote 解析以 " 开头的引号串，支持 \" 与 \\；未闭合返回 ok=false。
func unquote(s string) (string, bool) {
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			b.WriteByte(s[i+1])
			i++
			continue
		}
		if s[i] == '"' {
			return b.String(), true
		}
		b.WriteByte(s[i])
	}
	return b.String(), false
}

// Parse 解析单个媒体类型：type/subtype 小写，参数名小写、去引号并按名排序。
func Parse(s string) (*MediaType, error) {
	parts := splitQuoted(s, ';')
	head := strings.TrimSpace(parts[0])
	slash := strings.IndexByte(head, '/')
	if slash < 0 {
		return nil, &ParseError{0, ErrNoSlash}
	}
	mt := MediaType{Type: strings.ToLower(strings.TrimSpace(head[:slash])),
		Subtype: strings.ToLower(strings.TrimSpace(head[slash+1:]))}
	if mt.Type == "" || mt.Subtype == "" {
		return nil, &ParseError{0, ErrEmptySubtype}
	}
	pos := map[string]int{}
	for _, p := range parts[1:] {
		eq := strings.IndexByte(p, '=')
		if eq < 0 {
			return nil, &ParseError{0, ErrParamNoEquals}
		}
		name := strings.ToLower(strings.TrimSpace(p[:eq]))
		val := strings.TrimSpace(p[eq+1:])
		if strings.HasPrefix(val, `"`) {
			v, ok := unquote(val)
			if !ok {
				return nil, &ParseError{0, ErrUnclosedQuote}
			}
			val = v
		}
		if i, ok := pos[name]; ok {
			mt.Params[i].Value = val
		} else {
			pos[name] = len(mt.Params)
			mt.Params = append(mt.Params, Param{name, val})
		}
	}
	sort.Slice(mt.Params, func(i, j int) bool { return mt.Params[i].Name < mt.Params[j].Name })
	return &mt, nil
}

// EqualParams 判定两个参数集合（顺序无关，值大小写敏感）完全相等。
func EqualParams(a, b []Param) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
