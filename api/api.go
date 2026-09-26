// Package api 对外提供 Parse / Eval / ParseAndEval / SelfCheck，依赖 parse；无状态可并发。
package api

import (
	"errors"
	"ontology/lex"
	"ontology/parse"
)

var ErrDivZero = errors.New("api: division by constant zero") // 四类哨兵错误之一：常量除零
var kop = map[lex.Kind]string{lex.PLUS: parse.OpAdd, lex.MINUS: parse.OpSub, lex.STAR: parse.OpMul, lex.SLASH: parse.OpDiv}
var openPush = map[lex.Kind]lex.Kind{lex.LPAREN: lex.LPAREN, lex.MINUS: uMinus}

func applyOp(op string, a, b int64) (int64, error) {
	switch op {
	case parse.OpAdd:
		return a + b, nil
	case parse.OpSub:
		return a - b, nil
	case parse.OpMul:
		return a * b, nil
	case parse.OpDiv:
		if b == 0 {
			return 0, ErrDivZero
		}
		return a / b, nil
	}
	return 0, parse.ErrSyntax
}
func Parse(s string) (*parse.Node, error) {
	toks, err := lex.Tokenize(s)
	if err != nil {
		return nil, err
	}
	return parse.Parse(toks)
}
func Eval(n *parse.Node) (int64, error) {
	if n == nil {
		return 0, parse.ErrSyntax
	}
	if n.Op == parse.OpNum {
		return n.Val, nil
	}
	if n.Op == parse.OpNeg {
		v, err := Eval(n.Left)
		return -v, err
	}
	l, err := Eval(n.Left)
	if err != nil {
		return 0, err
	}
	r, err := Eval(n.Right)
	if err != nil {
		return 0, err
	}
	return applyOp(n.Op, l, r)
}
func ParseAndEval(s string) (int64, error) {
	n, err := Parse(s)
	if err != nil {
		return 0, err
	}
	return Eval(n)
}

const uMinus lex.Kind = 99 // 参照器内部一元负号记号（词法层不区分一元/二元）
// prec 为参照器显式优先级：LPAREN=-1，+ - =1，* / =2，一元 - =3。
var prec = map[lex.Kind]int{lex.LPAREN: -1, lex.PLUS: 1, lex.MINUS: 1, lex.STAR: 2, lex.SLASH: 2, uMinus: 3}

func referenceEval(toks []lex.Token) (res int64, err error) {
	defer func() {
		if e := recover(); e != nil {
			res, err = 0, e.(error)
		}
	}()
	var v []int64
	var o []lex.Kind
	pop := func() {
		k := o[len(o)-1]
		o = o[:len(o)-1]
		if k == uMinus {
			v[len(v)-1] = -v[len(v)-1]
			return
		}
		b, a := v[len(v)-1], v[len(v)-2]
		v = v[:len(v)-2]
		r, e := applyOp(kop[k], a, b)
		if e != nil {
			panic(e)
		}
		v = append(v, r)
	}
	red := func(lim int) { // 弹出栈顶优先级 >= lim 的运算符（>= 即左结合）
		for len(o) > 0 && prec[o[len(o)-1]] >= lim {
			pop()
		}
	}
	want := true
	for _, t := range toks[:len(toks)-1] { // 末尾 EOF 由 lex 保证存在，截掉免判
		if want {
			switch t.Kind {
			case lex.NUMBER:
				v, want = append(v, t.Val), false
			case lex.LPAREN, lex.MINUS:
				o = append(o, openPush[t.Kind])
			case lex.RPAREN:
				return 0, parse.ErrParen
			default:
				return 0, parse.ErrSyntax
			}
			continue
		}
		switch t.Kind {
		case lex.PLUS, lex.MINUS, lex.STAR, lex.SLASH:
			red(prec[t.Kind])
			o, want = append(o, t.Kind), true
		case lex.RPAREN:
			red(0) // 归约到 LPAREN(-1) 为止
			if len(o) == 0 {
				return 0, parse.ErrParen
			}
			o = o[:len(o)-1]
		default:
			return 0, parse.ErrTrailing
		}
	}
	if want {
		return 0, parse.ErrSyntax
	}
	red(0)
	if len(o) > 0 {
		return 0, parse.ErrParen // 未闭合的 LPAREN
	}
	return v[0], nil
}

// SelfCheck 核验不变量 1（与参照一致）与 2（AST 自洽）；3 左结合由 1 覆盖。
func SelfCheck() error {
	for _, s := range []string{"8/2/2-3", "2+3*4", "-7/2", "8-3-2", "- -3", "(1+2)*3", "-(2+3)*2", "7/-2", "2*-3", "---5", "(1/0)"} {
		direct, dErr := ParseAndEval(s)
		toks, _ := lex.Tokenize(s)
		ref, rErr := referenceEval(toks)
		n, _ := parse.Parse(toks)
		via, vErr := Eval(n)
		if dErr != rErr || (dErr == nil && (ref != direct || vErr != nil || via != direct)) {
			return errors.New("api: selfcheck mismatch: " + s)
		}
	}
	return nil
}
