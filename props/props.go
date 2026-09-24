// Package props 解析并回写 Java properties 文本（语义同 java.util.Properties）。
package props

import (
	"cmp"
	"fmt"
	"strings"

	"ontology/logical"
)

// Props 是有序键值映射：重复键后者覆盖，遍历按首次出现顺序。零值可用。
type Props struct {
	keys []string
	vals map[string]string
}

// Set 设置键值；新键追加到顺序末尾，已有键只覆盖值。
func (p *Props) Set(k, v string) {
	if p.vals == nil {
		p.vals = map[string]string{}
	}
	if _, ok := p.vals[k]; !ok {
		p.keys = append(p.keys, k)
	}
	p.vals[k] = v
}
func (p *Props) Get(k string) (string, bool) { v, ok := p.vals[k]; return v, ok }
func (p *Props) Len() int                    { return len(p.keys) }
func (p *Props) At(i int) (string, string)   { k := p.keys[i]; return k, p.vals[k] }

// EscapeError 是非法 \uXXXX 转义，Line/Col 为物理行号与列号（1 起始）。
type EscapeError struct{ Line, Col int }

func (e *EscapeError) Error() string {
	return fmt.Sprintf("props: malformed \\u escape at line %d, column %d", e.Line, e.Col)
}

// Load 解析 properties 文本，语义同 java.util.Properties.load。
func Load(src string) (*Props, error) {
	var sc logical.Scanner
	p := new(Props)
	for _, ln := range sc.Split([]byte(src)) {
		text, starts := ln.Flatten()
		pos := func(off int) (int, int) { return ln.Pos(starts, off) }
		kr, vr := splitKeyVal(text)
		key, kerr := unescape(kr, pos)
		val, verr := unescape(vr, func(o int) (int, int) { return pos(len(text) - len(vr) + o) })
		if kerr != nil || verr != nil {
			return nil, cmp.Or(kerr, verr)
		}
		p.Set(key, val)
	}
	return p, nil
}
func splitKeyVal(s string) (string, string) {
	prec := false
	keyEnd, valStart, hasSep := len(s), len(s), false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if sep := c == '=' || c == ':'; !prec && (sep || c == ' ' || c == '\t' || c == '\f') {
			keyEnd, valStart, hasSep = i, i+1, sep
			break
		}
		prec = c == '\\' && !prec
	}
	for valStart < len(s) {
		c := s[valStart]
		if c == ' ' || c == '\t' || c == '\f' || !hasSep && (c == '=' || c == ':') {
			hasSep = hasSep || c == '=' || c == ':'
			valStart++
			continue
		}
		break
	}
	return s[:keyEnd], s[valStart:]
}

func unescape(s string, pos func(int) (int, int)) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		if i++; i >= len(s) {
			b.WriteByte('\\')
			break
		}
		if j := strings.IndexByte("tnrf", s[i]); j >= 0 {
			b.WriteByte("\t\n\r\f"[j])
			continue
		}
		if s[i] != 'u' {
			b.WriteByte(s[i])
			continue
		}
		v := 0
		for j := i + 1; j <= i+4; j++ {
			if j >= len(s) || hexVal(s[j]) < 0 {
				l, c := pos(i - 1)
				return "", &EscapeError{l, c}
			}
			v = v<<4 | hexVal(s[j])
		}
		b.WriteRune(rune(v))
		i += 4
	}
	return b.String(), nil
}

func hexVal(c byte) int {
	switch c |= 32; {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	}
	return -1
}

// Store 把映射写成 properties 文本：只转义必要字符，非 ASCII 原样输出。
func (p *Props) Store() string {
	var b strings.Builder
	for _, k := range p.keys {
		fmt.Fprintf(&b, "%s=%s\n", escape(k, true), escape(p.vals[k], false))
	}
	return b.String()
}

func escape(s string, isKey bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' {
			b.WriteString(`\\`)
		} else if j := strings.IndexByte("\t\n\r\f", c); j >= 0 {
			b.WriteByte('\\')
			b.WriteByte("tnrf"[j])
		} else {
			if c == ' ' && (isKey || i == 0) || isKey && (c == '=' || c == ':' || (c == '#' || c == '!') && i == 0) {
				b.WriteByte('\\')
			}
			b.WriteByte(c)
		}
	}
	return b.String()
}
