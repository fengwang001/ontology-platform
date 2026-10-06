package callstack

import (
	"fmt"
	"strconv"
	"strings"
)

// parser.go 负责把小型 S 表达式源码解析为 AST，并完成两件载入期工作：
//  1. 词法作用域解析（变量 -> 帧内槽位），统计每个函数的槽位总数；
//  2. 调用 MarkTail 完成尾位置标注（之后运行时判定为 O(1)）。
//
// 语法（EBNF 风格）：
//   program := { "(" "def" name "(" {name} ")" expr ")" } expr
//   expr := int | name
//         | "(" name {expr} ")"                       调用
//         | "(" "if" expr expr expr ")"
//         | "(" "do" {expr}+ ")"                       顺序
//         | "(" "let" "(" name expr ")" expr ")"
//         | "(" "try" expr "(" "catch" name expr ")" ")"
//         | "(" "throw" expr ")"
//         | "(" ("+"|"-"|"*"|"/"|"<"|"=") expr expr ")"

type token struct {
	text string
	line int
}

type parser struct {
	tokens []token
	pos    int
}

func tokenize(src string) ([]token, error) {
	var toks []token
	line := 1
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '\n':
			line++
		case c == ' ' || c == '\t' || c == '\r':
		case c == '(' || c == ')':
			toks = append(toks, token{string(c), line})
		case c == ';':
			for i < len(src) && src[i] != '\n' {
				i++
			}
			line++
		default:
			start := i
			for i < len(src) {
				ch := src[i]
				if ch == '(' || ch == ')' || ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
					break
				}
				i++
			}
			toks = append(toks, token{src[start:i], line})
			i--
		}
	}
	return toks, nil
}

var keywords = map[string]bool{
	"def": true, "if": true, "do": true, "let": true,
	"try": true, "catch": true, "throw": true,
}

var binops = map[string]bool{
	"+": true, "-": true, "*": true, "/": true, "<": true, "=": true,
}

// scope 做载入期的变量名 -> 槽位解析。
type binding struct {
	name string
	slot int
}

type scope struct {
	bindings []binding
	nextSlot int
}

func newScope(params []string) *scope {
	sc := &scope{nextSlot: len(params)}
	for i, p := range params {
		sc.bindings = append(sc.bindings, binding{p, i})
	}
	return sc
}

func (sc *scope) lookup(name string) int {
	for i := len(sc.bindings) - 1; i >= 0; i-- {
		if sc.bindings[i].name == name {
			return sc.bindings[i].slot
		}
	}
	return -1
}

func (sc *scope) alloc(name string) int {
	slot := sc.nextSlot
	sc.nextSlot++
	sc.bindings = append(sc.bindings, binding{name, slot})
	return slot
}

func (sc *scope) release(n int) { sc.bindings = sc.bindings[:n] }

func (p *parser) peek() (token, bool) {
	if p.pos >= len(p.tokens) {
		return token{}, false
	}
	return p.tokens[p.pos], true
}

func (p *parser) next() (token, error) {
	if p.pos >= len(p.tokens) {
		return token{}, fmt.Errorf("unexpected end of input")
	}
	t := p.tokens[p.pos]
	p.pos++
	return t, nil
}

func (p *parser) expect(text string) error {
	t, err := p.next()
	if err != nil {
		return err
	}
	if t.text != text {
		return fmt.Errorf("line %d: expected %q, got %q", t.line, text, t.text)
	}
	return nil
}

func (p *parser) parseExpr(sc *scope) (Expr, error) {
	t, err := p.next()
	if err != nil {
		return nil, err
	}
	if t.text != "(" {
		if isInteger(t.text) {
			v, _ := strconv.ParseInt(t.text, 10, 64)
			return &NumExpr{Value: v}, nil
		}
		if keywords[t.text] || binops[t.text] {
			return nil, fmt.Errorf("line %d: unexpected keyword %q", t.line, t.text)
		}
		slot := sc.lookup(t.text)
		if slot < 0 {
			return nil, fmt.Errorf("line %d: unbound variable %q", t.line, t.text)
		}
		return &VarExpr{Name: t.text, Slot: slot}, nil
	}

	head, err := p.next()
	if err != nil {
		return nil, err
	}
	switch {
	case head.text == "if":
		cond, err := p.parseExpr(sc)
		if err != nil {
			return nil, err
		}
		then, err := p.parseExpr(sc)
		if err != nil {
			return nil, err
		}
		els, err := p.parseExpr(sc)
		if err != nil {
			return nil, err
		}
		if err := p.expect(")"); err != nil {
			return nil, err
		}
		return &IfExpr{Cond: cond, Then: then, Else: els}, nil
	case head.text == "do":
		var xs []Expr
		for {
			if t, ok := p.peek(); ok && t.text == ")" {
				p.pos++
				break
			}
			x, err := p.parseExpr(sc)
			if err != nil {
				return nil, err
			}
			xs = append(xs, x)
		}
		if len(xs) == 0 {
			return nil, fmt.Errorf("(do) requires at least one expression")
		}
		return &SeqExpr{Exprs: xs}, nil
	case head.text == "let":
		if err := p.expect("("); err != nil {
			return nil, err
		}
		nameTok, err := p.next()
		if err != nil {
			return nil, err
		}
		if keywords[nameTok.text] || binops[nameTok.text] {
			return nil, fmt.Errorf("line %d: invalid binding name %q", nameTok.line, nameTok.text)
		}
		init, err := p.parseExpr(sc)
		if err != nil {
			return nil, err
		}
		if err := p.expect(")"); err != nil {
			return nil, err
		}
		var slot int
		mark := len(sc.bindings)
		slot = sc.alloc(nameTok.text)
		body, err := p.parseExpr(sc)
		if err != nil {
			return nil, err
		}
		sc.release(mark)
		if err := p.expect(")"); err != nil {
			return nil, err
		}
		return &LetExpr{Name: nameTok.text, Slot: slot, Init: init, Body: body}, nil
	case head.text == "try":
		body, err := p.parseExpr(sc)
		if err != nil {
			return nil, err
		}
		if err := p.expect("("); err != nil {
			return nil, err
		}
		catchTok, err := p.next()
		if err != nil {
			return nil, err
		}
		if catchTok.text != "catch" {
			return nil, fmt.Errorf("line %d: expected catch, got %q", catchTok.line, catchTok.text)
		}
		nameTok, err := p.next()
		if err != nil {
			return nil, err
		}
		mark := len(sc.bindings)
		excSlot := sc.alloc(nameTok.text)
		handler, err := p.parseExpr(sc)
		if err != nil {
			return nil, err
		}
		sc.release(mark)
		if err := p.expect(")"); err != nil {
			return nil, err
		}
		if err := p.expect(")"); err != nil {
			return nil, err
		}
		return &TryExpr{Body: body, Handler: handler, ExcSlot: excSlot}, nil
	case head.text == "throw":
		arg, err := p.parseExpr(sc)
		if err != nil {
			return nil, err
		}
		if err := p.expect(")"); err != nil {
			return nil, err
		}
		return &ThrowExpr{Arg: arg}, nil
	case binops[head.text]:
		lhs, err := p.parseExpr(sc)
		if err != nil {
			return nil, err
		}
		rhs, err := p.parseExpr(sc)
		if err != nil {
			return nil, err
		}
		if err := p.expect(")"); err != nil {
			return nil, err
		}
		return &BinExpr{Op: head.text, Lhs: lhs, Rhs: rhs}, nil
	default:
		if keywords[head.text] {
			return nil, fmt.Errorf("line %d: unexpected keyword %q in call position", head.line, head.text)
		}
		c := &CallExpr{Name: head.text}
		for {
			if t, ok := p.peek(); ok && t.text == ")" {
				p.pos++
				break
			}
			a, err := p.parseExpr(sc)
			if err != nil {
				return nil, err
			}
			c.Args = append(c.Args, a)
		}
		return c, nil
	}
}

func isInteger(s string) bool {
	t := s
	if strings.HasPrefix(t, "-") {
		t = t[1:]
	}
	if t == "" {
		return false
	}
	for _, r := range t {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Parse 解析完整程序：若干 def 加一个主表达式。
func Parse(src string) (*Program, error) {
	toks, err := tokenize(src)
	if err != nil {
		return nil, err
	}
	p := &parser{tokens: toks}
	prog := &Program{Funcs: map[string]*Func{}}

	for {
		t, ok := p.peek()
		if !ok {
			return nil, fmt.Errorf("missing main expression")
		}
		if t.text != "(" {
			break
		}
		// 向前看两个 token 判断是否为 def。
		if p.pos+1 >= len(p.tokens) || p.tokens[p.pos+1].text != "def" {
			break
		}
		p.pos++ // (
		p.pos++ // def
		nameTok, err := p.next()
		if err != nil {
			return nil, err
		}
		if _, dup := prog.Funcs[nameTok.text]; dup {
			return nil, fmt.Errorf("line %d: duplicate function %q", nameTok.line, nameTok.text)
		}
		if err := p.expect("("); err != nil {
			return nil, err
		}
		var params []string
		for {
			tt, err := p.next()
			if err != nil {
				return nil, err
			}
			if tt.text == ")" {
				break
			}
			if keywords[tt.text] || binops[tt.text] {
				return nil, fmt.Errorf("line %d: bad parameter name %q", tt.line, tt.text)
			}
			params = append(params, tt.text)
		}
		sc := newScope(params)
		body, err := p.parseExpr(sc)
		if err != nil {
			return nil, err
		}
		if err := p.expect(")"); err != nil {
			return nil, err
		}
		prog.Funcs[nameTok.text] = &Func{
			Name:   nameTok.text,
			Params: append([]string(nil), params...),
			NSlots: sc.nextSlot,
			Body:   body,
		}
	}

	mainScope := newScope(nil)
	main, err := p.parseExpr(mainScope)
	if err != nil {
		return nil, err
	}
	if p.pos != len(p.tokens) {
		return nil, fmt.Errorf("trailing tokens after main expression")
	}
	prog.Main = main
	prog.MainSlots = mainScope.nextSlot
	MarkTail(prog)
	return prog, nil
}
