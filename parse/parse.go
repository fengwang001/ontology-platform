package parse

import "errors"
import "ontology/lex"

var ErrSyntax, ErrDuplicateKey = errors.New("parse: invalid syntax"), errors.New("parse: duplicate object key")

type parser struct {
	b      []byte
	i, cmp int
}

func (p *parser) ws() {
	for p.i < len(p.b) && isSpace[p.b[p.i]] {
		p.i++
	}
}
func (p *parser) value() (Value, error) {
	p.ws()
	if p.i >= len(p.b) {
		return Value{}, ErrSyntax
	}
	switch p.b[p.i] {
	case 'n', 't', 'f':
		return p.lit()
	case '"':
		return p.str()
	case '[', '{':
		return p.seq()
	default:
		return p.number()
	}
}
func (p *parser) lit() (Value, error) {
	w, k := "null", KindNull
	if p.b[p.i] == 't' {
		w, k = "true", KindTrue
	} else if p.b[p.i] == 'f' {
		w, k = "false", KindFalse
	}
	if p.i+len(w) > len(p.b) || string(p.b[p.i:p.i+len(w)]) != w {
		return Value{}, ErrSyntax
	}
	p.i += len(w)
	return Value{Kind: k}, nil
}
func (p *parser) str() (Value, error) {
	st := p.i
	for j := st + 1; j < len(p.b); j++ {
		if p.b[j] == '\\' {
			j++
			continue
		}
		if p.b[j] == '"' {
			s, err := lex.ParseString(p.b[st : j+1])
			if err != nil {
				return Value{}, err
			}
			p.i = j + 1
			return Value{Kind: KindString, Str: s}, nil
		}
	}
	return Value{}, ErrSyntax
}
func (p *parser) number() (Value, error) {
	st := p.i
	for p.i < len(p.b) && isNumTok[p.b[p.i]] {
		p.i++
	}
	if p.i == st {
		return Value{}, ErrSyntax
	}
	v, err := lex.ParseNumber(string(p.b[st:p.i]))
	if err != nil {
		return Value{}, err
	}
	return Value{Kind: KindNumber, Num: v}, nil
}
func (p *parser) seq() (Value, error) {
	isObj := p.b[p.i] == '{'
	cl := p.b[p.i] + 2 // '['+2 == ']', '{' +2 == '}'
	p.i++
	arr, obj := []Value{}, map[string]Value{}
	for {
		p.ws()
		if p.i < len(p.b) && p.b[p.i] == cl {
			p.i++
			break
		}
		key := ""
		if isObj {
			if p.i >= len(p.b) || p.b[p.i] != '"' {
				return Value{}, ErrSyntax
			}
			kv, err := p.str()
			if err != nil {
				return Value{}, err
			}
			key = kv.Str
			p.ws()
			if p.i >= len(p.b) || p.b[p.i] != ':' {
				return Value{}, ErrSyntax
			}
			p.i++
		}
		v, err := p.value()
		if err != nil {
			return Value{}, err
		}
		if isObj {
			p.cmp++
			if _, dup := obj[key]; dup {
				return Value{}, ErrDuplicateKey
			}
			obj[key] = v
		} else {
			arr = append(arr, v)
		}
		p.ws()
		if p.i >= len(p.b) {
			return Value{}, ErrSyntax
		}
		c := p.b[p.i]
		p.i++
		if c == cl {
			break
		}
		if c != ',' {
			return Value{}, ErrSyntax
		}
	}
	if isObj {
		return Value{Kind: KindObject, Obj: obj}, nil
	}
	return Value{Kind: KindArray, Arr: arr}, nil
}
func Parse(b []byte) (Value, error) { v, _, e := parseCounted(b); return v, e }
func parseCounted(b []byte) (Value, int, error) {
	p := &parser{b: b}
	v, err := p.value()
	p.ws()
	if err == nil && p.i != len(b) {
		err = ErrSyntax
	}
	if err != nil {
		return Value{}, p.cmp, err
	}
	return v, p.cmp, nil
}
