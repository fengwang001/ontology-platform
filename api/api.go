// Package api 对外提供解析与求值入口，并内置两栈参照求值器用于自检。依赖 parse。
package api

import (
	"errors"
	"fmt"
	"ontology/lex"
	"ontology/parse"
)

// ErrDivZero 常量除零：二元 / 的右操作数求值为 0。
var ErrDivZero = errors.New("api: division by constant zero")

// Parse 把表达式字符串解析成 AST。
func Parse(s string) (*parse.Node, error) {
	toks, err := lex.Lex(s)
	if err != nil {
		return nil, err
	}
	return parse.Parse(toks)
}

// Eval 对 AST 求值；只读 Node，不依赖解析过程之外的任何信息。/ 向零截断。
func Eval(n *parse.Node) (int64, error) {
	if n.Op == parse.Num {
		return n.Val, nil
	}
	l, err := Eval(n.Left)
	if err != nil {
		return 0, err
	}
	if n.Op == parse.Neg {
		return -l, nil
	}
	r, err := Eval(n.Right)
	if err != nil {
		return 0, err
	}
	return applyOp(n.Op, l, r)
}

func applyOp(op parse.Op, l, r int64) (int64, error) {
	switch op {
	case parse.Add:
		return l + r, nil
	case parse.Sub:
		return l - r, nil
	case parse.Mul:
		return l * r, nil
	case parse.Div:
		if r == 0 {
			return 0, ErrDivZero
		}
		return l / r, nil
	}
	return 0, fmt.Errorf("api: bad op %d", op)
}

// ParseAndEval 解析并求值。
func ParseAndEval(s string) (int64, error) {
	n, err := Parse(s)
	if err != nil {
		return 0, err
	}
	return Eval(n)
}

// refEval 是独立的两栈算符优先参照求值器：显式优先级表（+、- 为 1，*、/ 为 2，一元 - 为 3），二元左结合。仅用于对拍。
func refEval(s string) (int64, error) {
	toks, _ := lex.Lex(s) // 调用方只传合法表达式
	const lpar, rpar parse.Op = 6, 7
	prec := [8]int{0, 1, 1, 2, 2, 3, -1, 0} // 显式优先级表：+ -=1，* /=2，一元 -=3
	var vals []int64
	var ops []parse.Op
	apply := func() error {
		o := ops[len(ops)-1]
		ops = ops[:len(ops)-1]
		if o == parse.Neg {
			vals[len(vals)-1] = -vals[len(vals)-1]
			return nil
		}
		r, l := vals[len(vals)-1], vals[len(vals)-2]
		vals = vals[:len(vals)-2]
		v, err := applyOp(o, l, r)
		if err != nil {
			return err
		}
		vals = append(vals, v)
		return nil
	}
	wantNum := true
	for _, t := range toks {
		if t.Kind == lex.Number {
			vals = append(vals, t.Val)
		} else if t.Kind == lex.LParen {
			ops = append(ops, lpar)
		} else {
			o := parse.Op(t.Kind) // lex 与 parse 的 + - * / 序号一致
			if t.Kind == lex.RParen {
				o = rpar
			} else if wantNum && t.Kind == lex.Sub {
				o = parse.Neg
			}
			for len(ops) > 0 && (prec[ops[len(ops)-1]] > prec[o] || o >= parse.Add && o <= parse.Div && prec[ops[len(ops)-1]] == prec[o]) {
				if err := apply(); err != nil {
					return 0, err
				}
			}
			if o == rpar {
				if len(ops) == 0 {
					return 0, parse.ErrParen
				}
				ops = ops[:len(ops)-1]
			} else {
				ops = append(ops, o)
			}
		}
		wantNum = t.Kind != lex.Number && t.Kind != lex.RParen
	}
	for len(ops) > 0 {
		if err := apply(); err != nil {
			return 0, err
		}
	}
	if len(vals) != 1 {
		return 0, parse.ErrOperand
	}
	return vals[0], nil
}

// SelfCheck 对一组内置表达式核验四条不变量，全部通过才返回 nil。
func SelfCheck() error {
	cases := map[string]int64{"8/2/2-3": -1, "2+3*4": 14, "-7/2": -3, "8-3-2": 3, "- -3": 3, "(1+2)*3": 9, "-2*3": -6, "7/-2": -3, "1-2-3-4": -8, "0-5/2": -2}
	for s, want := range cases {
		n, _ := Parse(s)
		v, err := Eval(n)       // 不变量 2：AST 与求值自洽
		ref, rerr := refEval(s) // 不变量 1、3：与两栈左结合参照一致
		if err != nil || rerr != nil || v != want || ref != want {
			return fmt.Errorf("selfcheck %q: eval=%d ref=%d want=%d", s, v, ref, want)
		}
	}
	for i, s := range []string{"1@2", "(1", "1/0", "", "1 2", "2+3*4"} { // 不变量 4：失败不留痕
		v, err := ParseAndEval(s)
		if ok := err == nil; ok != (i == 5) || ok && v != 14 {
			return fmt.Errorf("selfcheck: bad result for %q", s)
		}
	}
	return nil
}
