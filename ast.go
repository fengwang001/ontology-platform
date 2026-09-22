package ontology

import "math/big"

// Node 是解析树的节点。String 输出规范化的带括号结构，
// 同一表达式字符串只可能产生一种结构，可用于断言解析唯一性。
type Node interface {
	String() string
	eval() (*big.Rat, error)
}

// numNode 是数字字面量，rat 为其精确有理数值。
type numNode struct {
	text string
	rat  *big.Rat
}

func (n *numNode) String() string { return n.text }

func (n *numNode) eval() (*big.Rat, error) {
	return new(big.Rat).Set(n.rat), nil
}

// negNode 是一元负号，优先级高于乘除。
type negNode struct {
	x Node
}

func (n *negNode) String() string {
	if _, ok := n.x.(*numNode); ok {
		return "-" + n.x.String()
	}
	return "-(" + n.x.String() + ")"
}

func (n *negNode) eval() (*big.Rat, error) {
	v, err := n.x.eval()
	if err != nil {
		return nil, err
	}
	return v.Neg(v), nil
}

// binNode 是二元运算，op 为 + - * / 之一，pos 为运算符字节位置。
type binNode struct {
	op  byte
	l   Node
	r   Node
	pos int
}

func (n *binNode) String() string {
	return "(" + n.l.String() + string(n.op) + n.r.String() + ")"
}

func (n *binNode) eval() (*big.Rat, error) {
	l, err := n.l.eval()
	if err != nil {
		return nil, err
	}
	r, err := n.r.eval()
	if err != nil {
		return nil, err
	}
	switch n.op {
	case '+':
		return l.Add(l, r), nil
	case '-':
		return l.Sub(l, r), nil
	case '*':
		return l.Mul(l, r), nil
	case '/':
		if r.Sign() == 0 {
			return nil, errAt(ErrDivZero, n.pos, "除数为零")
		}
		return l.Quo(l, r), nil
	}
	return nil, errPlain(ErrIllegalChar, "未知运算符 %q", n.op)
}
