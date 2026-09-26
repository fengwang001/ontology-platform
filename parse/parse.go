// Package parse 按题目给定文法递归下降构建 AST，依赖 lex。
// 二元运算左结合（循环内左折叠）。解析器单次使用，无包级可变状态。
package parse

import (
	"errors"

	"ontology/lex"
)

// AST 运算符；叶子 OpNum（值在 Val），一元负号 OpNeg（子节点在 Left）。
const (
	OpNum = "num"
	OpAdd = "+"
	OpSub = "-"
	OpMul = "*"
	OpDiv = "/"
	OpNeg = "neg"
)

// Node 是导出的 AST 节点：叶子存 Val，内部节点存运算符与子节点。
type Node struct {
	Op          string
	Val         int64
	Left, Right *Node
}

// parse 层哨兵错误，彼此不同且与 lex/api 层不同。
var (
	ErrEmpty    = errors.New("parse: empty expression")  // 空表达式
	ErrParen    = errors.New("parse: mismatched parens") // 括号不配对
	ErrTrailing = errors.New("parse: trailing tokens")   // 未消费完
	ErrSyntax   = errors.New("parse: syntax error")      // 其它结构错误
)

// binOp 为只读映射，多 goroutine 共享安全。
var binOp = map[lex.Kind]string{
	lex.PLUS: OpAdd, lex.MINUS: OpSub, lex.STAR: OpMul, lex.SLASH: OpDiv,
}

type parser struct {
	src  []lex.Token
	pos  int
	buf  []lex.Token // 前瞻缓冲：LL(1) 下长度恒为 0 或 1
	peak int         // 非导出计数器：缓冲保留记号数的峰值
}

func (p *parser) peek() lex.Token {
	if len(p.buf) == 0 {
		t := lex.Token{Kind: lex.EOF}
		if p.pos < len(p.src) {
			t, p.pos = p.src[p.pos], p.pos+1
		}
		p.buf = append(p.buf, t)
		p.peak = max(p.peak, len(p.buf))
	}
	return p.buf[0]
}

func (p *parser) next() lex.Token {
	t := p.peek()
	p.buf = p.buf[:0]
	return t
}

func (p *parser) factor() (*Node, error) {
	switch p.peek().Kind {
	case lex.NUMBER:
		t := p.next()
		return &Node{Op: OpNum, Val: t.Val}, nil
	case lex.LPAREN:
		p.next()
		n, err := p.expr()
		if err != nil {
			if p.peek().Kind != lex.EOF {
				return nil, err
			}
		} else if p.peek().Kind != lex.RPAREN {
			err = ErrParen
		}
		if err != nil {
			return nil, ErrParen
		}
		p.next()
		return n, nil
	case lex.MINUS:
		p.next()
		sub, err := p.factor()
		if err != nil {
			return nil, err
		}
		return &Node{Op: OpNeg, Left: sub}, nil
	case lex.RPAREN:
		return nil, ErrParen // 右括号多于左括号
	default:
		return nil, ErrSyntax
	}
}

func (p *parser) fold(next func() (*Node, error), a, b lex.Kind) (*Node, error) {
	left, err := next()
	if err != nil {
		return nil, err
	}
	for k := p.peek().Kind; k == a || k == b; k = p.peek().Kind {
		p.next()
		right, err := next()
		if err != nil {
			return nil, err
		}
		left = &Node{Op: binOp[k], Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) term() (*Node, error) { return p.fold(p.factor, lex.STAR, lex.SLASH) }
func (p *parser) expr() (*Node, error) { return p.fold(p.term, lex.PLUS, lex.MINUS) }

// Parse 把记号流解析成 AST；空、括号不配对、尾部多余记号均整体失败。
func Parse(toks []lex.Token) (*Node, error) {
	if len(toks) == 0 || toks[0].Kind == lex.EOF {
		return nil, ErrEmpty
	}
	p := &parser{src: toks}
	n, err := p.expr()
	if err != nil {
		return nil, err
	}
	switch k := p.peek().Kind; {
	case k == lex.EOF:
		return n, nil
	case k == lex.RPAREN:
		return nil, ErrParen
	default:
		return nil, ErrTrailing
	}
}

// VerifyLL1 仅返回「前瞻峰值是否不超过 1」的布尔判定，不暴露计数器数值。
func VerifyLL1(s string) bool {
	toks, err := lex.Tokenize(s)
	if err != nil {
		return false
	}
	p := &parser{src: toks}
	if _, err := p.expr(); err != nil {
		return false
	}
	return p.peak <= 1
}
