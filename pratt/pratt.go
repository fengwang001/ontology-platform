// Package pratt 用优先爬升（Pratt / precedence climbing）解析表达式并求值。
package pratt

import "errors"
import "ontology/lex"

type Node struct {
	Op   lex.Kind // 运算种类，复用记号种类；Neg 表示前缀一元 -
	Val  int64
	L, R *Node // Neg 只用 L，二元运算用 L 和 R
}

var ErrSyntax = errors.New("pratt: syntax error")
var ErrParen = errors.New("pratt: unbalanced parenthesis")
var ErrDivZero = errors.New("pratt: division by constant zero")
var ErrBadExp = errors.New("pratt: illegal exponent")
var ErrOverflow = errors.New("pratt: power overflow")

const unaryBP = 40 // 前缀一元 - 解析其操作数时传入的 min_bp
const Neg = lex.Kind(100)

// 绑定力表（不许改）：中缀 {lbp, rbp}。
var infixBP = map[lex.Kind][2]int{
	lex.Add: {10, 11}, lex.Sub: {10, 11}, lex.Mul: {20, 21},
	lex.Div: {20, 21}, lex.Pow: {30, 30},
}

type parser struct {
	toks []lex.Token
	pos  int
	cmps int // 非导出：循环条件里比较过绑定力的中缀运算符个数
}

// Parse 把记号流解析成 AST；内部以 panic 收敛递归错误，对外只暴露哨兵错误。
func Parse(toks []lex.Token) (n *Node, err error) {
	p := &parser{toks: toks}
	defer func() {
		if r := recover(); r != nil {
			n, err = nil, r.(error)
		}
	}()
	n = p.parseExpr(0)
	if k := p.toks[p.pos].Kind; k == lex.RParen {
		return nil, ErrParen
	} else if k != lex.EOF {
		return nil, ErrSyntax
	}
	return n, nil
}

// parseExpr 是优先爬升主循环：先读前缀，再在 lbp >= minBP 时吞中缀。
func (p *parser) parseExpr(minBP int) *Node {
	t := p.toks[p.pos]
	p.pos++
	var lhs *Node
	switch t.Kind {
	case lex.Num:
		lhs = &Node{Op: lex.Num, Val: t.Val}
	case lex.Sub: // 前缀一元 -
		lhs = &Node{Op: Neg, L: p.parseExpr(unaryBP)}
	case lex.LParen:
		lhs = p.parseExpr(0)
		if p.toks[p.pos].Kind != lex.RParen {
			panic(ErrParen)
		}
		p.pos++
	default: // EOF 或无法作为前缀的记号
		panic(ErrSyntax)
	}
	for {
		k := p.toks[p.pos].Kind
		bp, ok := infixBP[k]
		if !ok {
			break // 下一个不是中缀运算符，本层结束
		}
		p.cmps++
		if bp[0] < minBP {
			break // 绑定力不足，提前停止
		}
		p.pos++
		lhs = &Node{Op: k, L: lhs, R: p.parseExpr(bp[1])}
	}
	return lhs
}
func Eval(n *Node) (int64, error) {
	if n.Op == lex.Num {
		return n.Val, nil
	}
	l, err := Eval(n.L)
	if err != nil {
		return 0, err
	}
	if n.Op == Neg {
		return -l, nil
	}
	r, err := Eval(n.R)
	if err != nil {
		return 0, err
	}
	switch n.Op {
	case lex.Add:
		return l + r, nil
	case lex.Sub:
		return l - r, nil
	case lex.Mul:
		return l * r, nil
	case lex.Div:
		if r == 0 {
			return 0, ErrDivZero
		}
		return l / r, nil // Go 整数除法即向零截断
	case lex.Pow:
		return pow(l, r)
	}
	return 0, ErrSyntax
}

// pow 计算 a^b：b>=0 且可表示为 int，0^0=1；除 0^0 外溢出即失败。
func pow(a, b int64) (int64, error) {
	if b < 0 || int64(int(b)) != b {
		return 0, ErrBadExp
	}
	result, base := int64(1), a
	for exp := b; exp > 0; exp >>= 1 {
		if exp&1 == 1 {
			r, err := mul(result, base)
			if err != nil {
				return 0, err
			}
			result = r
		}
		if exp > 1 {
			b2, err := mul(base, base)
			if err != nil {
				return 0, err
			}
			base = b2
		}
	}
	return result, nil
}

// mul 是带溢出检测的 int64 乘法（除法回验，MinInt*-1 由双向校验覆盖）。
func mul(a, b int64) (int64, error) {
	r := a * b
	if a != 0 && r/a != b || b != 0 && r/b != a {
		return 0, ErrOverflow
	}
	return r, nil
}
