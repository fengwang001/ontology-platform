// Package api 对外提供表达式求值入口与自检。
package api

import (
	"fmt"
	"ontology/lex"
	"ontology/pratt"
)

// Parse 把表达式字符串解析成 AST。
func Parse(s string) (*pratt.Node, error) {
	toks, err := lex.Tokenize(s)
	if err != nil {
		return nil, err
	}
	return pratt.Parse(toks)
}

// Eval 解析并求值，返回 int64。
func Eval(s string) (int64, error) {
	n, err := Parse(s)
	if err != nil {
		return 0, err
	}
	return pratt.Eval(n)
}

// EvalNode 对已解析的 AST 求值。
func EvalNode(n *pratt.Node) (int64, error) {
	return pratt.Eval(n)
}

// SelfCheck 对一组内置表达式核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	good := []string{
		"10-4-3", "2^3^2", "-2^2", "2^10", "- -3", "1- -2", "(1+2)^2",
		"7/2", "0-7/2", "2*3+4*5", "1+2*3^2", "2^(1+1)", "((5))", "3-2-1",
	}
	for _, s := range good { // 不变量 1：与朴素参照一致
		v1, e1 := Eval(s)
		v2, e2 := refEval(s)
		if (e1 == nil) != (e2 == nil) || (e1 == nil && v1 != v2) {
			return fmt.Errorf("api: selfcheck 不变量1 %q", s)
		}
	}
	if v, err := Eval("2^3^2"); err != nil || v != 512 { // 不变量 2：幂右结合
		return fmt.Errorf("api: selfcheck 不变量2")
	}
	if v, _ := Eval("- -3"); v != 3 { // 不变量 3：前缀/中缀 - 不冲突
		return fmt.Errorf("api: selfcheck 不变量3")
	}
	if v, _ := Eval("1- -3"); v != 4 {
		return fmt.Errorf("api: selfcheck 不变量3")
	}
	for _, s := range []string{"1+a", "(1+2", "1+2)", "2^-1", "1/0", "10^19", "", "1+"} {
		if _, err := Eval(s); err == nil { // 不变量 4：失败不留痕
			return fmt.Errorf("api: selfcheck 不变量4: %q 被接受", s)
		}
	}
	if v, err := Eval("1+1"); err != nil || v != 2 { // 被拒后状态不变
		return fmt.Errorf("api: selfcheck: 拒绝后状态被污染")
	}
	return nil
}

// refParser 是独立的硬编码优先级递归下降参照：显式括号化出 AST 后用同一 pratt.Eval 求值。
type refParser struct {
	toks []lex.Token
	pos  int
}

func refEval(s string) (int64, error) {
	toks, err := lex.Tokenize(s)
	if err != nil {
		return 0, err
	}
	r := &refParser{toks: toks}
	n, err := r.expr()
	if err != nil {
		return 0, err
	}
	if r.toks[r.pos].Kind != lex.EOF {
		return 0, pratt.ErrSyntax
	}
	return pratt.Eval(n)
}

// expr := term (('+'|'-') term)*；term := factor (('*'|'/') factor)*，均左结合。
func (r *refParser) expr() (*pratt.Node, error) { return r.leftAssoc(r.term, lex.Add, lex.Sub) }
func (r *refParser) term() (*pratt.Node, error) { return r.leftAssoc(r.factor, lex.Mul, lex.Div) }

func (r *refParser) leftAssoc(next func() (*pratt.Node, error), k1, k2 lex.Kind) (*pratt.Node, error) {
	l, err := next()
	if err != nil {
		return nil, err
	}
	for k := r.toks[r.pos].Kind; k == k1 || k == k2; k = r.toks[r.pos].Kind {
		r.pos++
		rhs, err := next()
		if err != nil {
			return nil, err
		}
		l = &pratt.Node{Op: k, L: l, R: rhs}
	}
	return l, nil
}

// factor := unary ('^' factor)? —— 幂右结合。
func (r *refParser) factor() (*pratt.Node, error) {
	base, err := r.unary()
	if err != nil {
		return nil, err
	}
	if r.toks[r.pos].Kind != lex.Pow {
		return base, nil
	}
	r.pos++
	exp, err := r.factor()
	if err != nil {
		return nil, err
	}
	return &pratt.Node{Op: lex.Pow, L: base, R: exp}, nil
}

// unary := '-' unary | primary —— 前缀负号绑定紧于 ^（(-2)^2=4）。
func (r *refParser) unary() (*pratt.Node, error) {
	t := r.toks[r.pos]
	r.pos++
	switch t.Kind {
	case lex.Sub:
		n, err := r.unary()
		if err != nil {
			return nil, err
		}
		return &pratt.Node{Op: pratt.Neg, L: n}, nil
	case lex.Num:
		return &pratt.Node{Op: lex.Num, Val: t.Val}, nil
	case lex.LParen:
		n, err := r.expr()
		if err != nil {
			return nil, err
		}
		if r.toks[r.pos].Kind != lex.RParen {
			return nil, pratt.ErrParen
		}
		r.pos++
		return n, nil
	}
	return nil, pratt.ErrSyntax
}
