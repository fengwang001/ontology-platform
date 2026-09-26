// Package parse 把 JSON 严格子集文本解析为内存值，并可再序列化。
package parse

import "strings"

type parser struct {
	b                 []byte
	i                 int
	lastCmp, totalCmp int // 判重比较计数：最近一次插入 / 累计
}

// Parse 整体解析一段文本；任何错误都返回零值，不留部分结果。
func Parse(b []byte) (Value, error) {
	p := &parser{b: b}
	v, err := p.value()
	p.ws()
	if err == nil && p.i != len(p.b) {
		err = ErrSyntax
	}
	if err != nil {
		return Value{}, err
	}
	return v, nil
}

func (p *parser) ws() {
	for p.i < len(p.b) && strings.IndexByte(" \t\n\r", p.b[p.i]) >= 0 {
		p.i++
	}
}

func (p *parser) value() (Value, error) {
	p.ws()
	if p.i >= len(p.b) {
		return Value{}, ErrSyntax
	}
	switch c := p.b[p.i]; c {
	case 'n':
		return p.lit("null", Value{Kind: Null})
	case 't':
		return p.lit("true", Value{Kind: Bool, Num: 1})
	case 'f':
		return p.lit("false", Value{Kind: Bool})
	case '"':
		s, err := p.str()
		if err != nil {
			return Value{}, err
		}
		return Value{Kind: Str, Str: s}, nil
	case '[':
		return p.arr()
	case '{':
		return p.obj()
	default:
		if c == '-' || '0' <= c && c <= '9' {
			return p.num()
		}
	}
	return Value{}, ErrSyntax
}

func (p *parser) lit(s string, v Value) (Value, error) {
	if len(p.b)-p.i < len(s) || string(p.b[p.i:p.i+len(s)]) != s {
		return Value{}, ErrSyntax
	}
	p.i += len(s)
	return v, nil
}

func (p *parser) expect(c byte) error {
	p.ws()
	if p.i >= len(p.b) || p.b[p.i] != c {
		return ErrSyntax
	}
	p.i++
	return nil
}

func (p *parser) arr() (Value, error) {
	p.i++
	v := Value{Kind: Arr}
	if p.expect(']') == nil {
		return v, nil
	}
	for {
		e, err := p.value()
		if err != nil {
			return Value{}, err
		}
		v.Arr = append(v.Arr, e)
		if p.expect(']') == nil {
			return v, nil
		}
		if err := p.expect(','); err != nil {
			return Value{}, err
		}
	}
}

func (p *parser) obj() (Value, error) {
	p.i++
	v := Value{Kind: Obj, Obj: map[string]Value{}}
	ks := map[uint64][]string{} // 哈希桶：判重定位 O(1)
	if p.expect('}') == nil {
		return v, nil
	}
	for {
		p.ws()
		if p.i >= len(p.b) || p.b[p.i] != '"' {
			return Value{}, ErrSyntax
		}
		k, err := p.str()
		if err != nil {
			return Value{}, err
		}
		h := fnv(k)
		p.lastCmp = 0
		for _, e := range ks[h] {
			p.lastCmp++
			p.totalCmp++
			if e == k {
				return Value{}, ErrDupKey
			}
		}
		ks[h] = append(ks[h], k)
		if err := p.expect(':'); err != nil {
			return Value{}, err
		}
		e, err := p.value()
		if err != nil {
			return Value{}, err
		}
		v.Obj[k] = e
		if p.expect('}') == nil {
			return v, nil
		}
		if err := p.expect(','); err != nil {
			return Value{}, err
		}
	}
}
