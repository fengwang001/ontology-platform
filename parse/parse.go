// Package parse 按 LL(1) 文法把记号流递归下降解析成 AST。依赖 lex。
// expr := term (('+'|'-') term)*；term := factor (('*'|'/') factor)*；
// factor := NUMBER | '(' expr ')' | '-' factor。
package parse

import (
	"errors"
	"sync/atomic"

	"ontology/lex"
)

// 可判定哨兵错误，互不相同，也不同于 lex.ErrLexical 与 api 的除零错误。
var (
	ErrOperand  = errors.New("parse: empty expression or missing operand")
	ErrTrailing = errors.New("parse: tokens left after expression")
	ErrParen    = errors.New("parse: unbalanced parentheses")
)

// Op 是 AST 节点种类。
type Op int

const (
	Num Op = iota // 叶子，值在 Val
	Add
	Sub
	Mul
	Div
	Neg // 一元负号，唯一子节点在 Left
)

// Node 是导出的 AST 结构：叶子存值（或变量名），内部节点存运算符与子节点。
type Node struct {
	Op    Op
	Val   int64  // 仅 Num 使用
	Name  string // 变量名预留，本文法无变量
	Left  *Node
	Right *Node // 仅二元运算使用
}

// peak 是非导出的前瞻缓冲峰值计数器，不出现于公开接口，仅本包测试可直读。
var peak atomic.Int64

// parser 的前瞻缓冲只有 1 个槽位，证明 LL(1) 单记号前瞻。
type parser struct {
	toks []lex.Token
	pos  int
	buf  [1]lex.Token
	n    int // buf 中保留的记号数，0 或 1
	max  int // 本次解析的峰值
}

func (p *parser) peek() (lex.Token, bool) {
	if p.n == 0 {
		if p.pos >= len(p.toks) {
			return lex.Token{}, false
		}
		p.buf[0], p.pos, p.n = p.toks[p.pos], p.pos+1, 1
		if p.n > p.max {
			p.max = p.n
		}
	}
	return p.buf[0], true
}

func (p *parser) take() lex.Token {
	t, _ := p.peek()
	p.n = 0
	return t
}

// Parse 把记号流解析成 AST；空流、括号不配对、有剩余记号都报错。
func Parse(toks []lex.Token) (*Node, error) {
	if len(toks) == 0 {
		return nil, ErrOperand
	}
	p := &parser{toks: toks}
	root, err := p.expr()
	if err != nil {
		return nil, err
	}
	if t, ok := p.peek(); ok {
		if t.Kind == lex.RParen {
			return nil, ErrParen // 右括号多于左括号
		}
		return nil, ErrTrailing
	}
	peak.Store(int64(p.max)) // 仅成功时记录峰值，失败不留痕
	return root, nil
}

var addOps = map[lex.Kind]Op{lex.Add: Add, lex.Sub: Sub}
var mulOps = map[lex.Kind]Op{lex.Mul: Mul, lex.Div: Div}

// level 是 expr/term 共用的左结合循环：向左累积新节点。
func (p *parser) level(sub func() (*Node, error), ops map[lex.Kind]Op) (*Node, error) {
	left, err := sub()
	if err != nil {
		return nil, err
	}
	for {
		t, ok := p.peek()
		op, isOp := ops[t.Kind]
		if !ok || !isOp {
			return left, nil
		}
		p.take()
		right, err := sub()
		if err != nil {
			return nil, err
		}
		left = &Node{Op: op, Left: left, Right: right}
	}
}

func (p *parser) expr() (*Node, error) { return p.level(p.term, addOps) }
func (p *parser) term() (*Node, error) { return p.level(p.factor, mulOps) }

func (p *parser) factor() (*Node, error) {
	t, ok := p.peek()
	if !ok {
		return nil, ErrOperand // 表达式戛然而止
	}
	switch t.Kind {
	case lex.Number:
		p.take()
		return &Node{Op: Num, Val: t.Val}, nil
	case lex.Sub: // 一元负号，优先级高于 * /
		p.take()
		c, err := p.factor()
		if err != nil {
			return nil, err
		}
		return &Node{Op: Neg, Left: c}, nil
	case lex.LParen:
		p.take()
		n, err := p.expr()
		if err != nil {
			return nil, err
		}
		if t, ok := p.peek(); !ok || t.Kind != lex.RParen {
			return nil, ErrParen // 左括号多于右括号
		}
		p.take()
		return n, nil
	case lex.RParen:
		return nil, ErrParen // 空括号或多余的右括号
	}
	return nil, ErrOperand // 期待操作数，却遇到运算符
}
