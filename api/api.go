package api

import (
	"fmt"
	"maps"
	"strconv"

	"ontology/ast"
	"ontology/tac"
)

func GenExpr(n *ast.Expr) ([]tac.Instr, error) { return tac.Gen(n) }

func truthy(x any) bool { b, ok := x.(bool); return b || !ok && x.(int64) != 0 }

func parseLit(s string) any {
	if v, e := strconv.ParseInt(s, 10, 64); e == nil {
		return v
	}
	return s == "true"
}

// binFns: "/" by zero panics, which proves short-circuited code never runs.
var binFns = map[string]func(any, any) any{"+": func(x, y any) any { return x.(int64) + y.(int64) }, "-": func(x, y any) any { return x.(int64) - y.(int64) }, "*": func(x, y any) any { return x.(int64) * y.(int64) }, "/": func(x, y any) any { return x.(int64) / y.(int64) }, "<": func(x, y any) any { return x.(int64) < y.(int64) }, ">": func(x, y any) any { return x.(int64) > y.(int64) }, "<=": func(x, y any) any { return x.(int64) <= y.(int64) }, ">=": func(x, y any) any { return x.(int64) >= y.(int64) }, "==": func(x, y any) any { return x == y }, "!=": func(x, y any) any { return x != y }}

// evalAST is the direct recursive reference semantics: short-circuit,
// strict left-to-right, left-associative.
func evalAST(n *ast.Expr, env map[string]any) any {
	switch n.Kind {
	case ast.IntLit:
		return n.Val
	case ast.BoolLit:
		return n.BVal
	case ast.Var:
		return env[n.Name]
	case ast.Unary:
		v := evalAST(n.Left, env)
		if n.Op == "-" {
			return -v.(int64)
		}
		return !truthy(v)
	case ast.Binary:
		if n.Op == "&&" || n.Op == "||" {
			l := evalAST(n.Left, env)
			if truthy(l) != (n.Op == "&&") {
				return l
			}
			return evalAST(n.Right, env)
		}
		return binFns[n.Op](evalAST(n.Left, env), evalAST(n.Right, env))
	case ast.If:
		if truthy(evalAST(n.Cond, env)) {
			return evalAST(n.Left, env)
		}
		return evalAST(n.Right, env)
	}
	return nil
}

// runTAC is the naive per-instruction interpreter; jumps decide what runs.
func runTAC(is []tac.Instr, env map[string]any) any {
	labels := map[string]int{}
	for i, x := range is {
		if x.Op == tac.OpLabel {
			labels[x.Label] = i
		}
	}
	regs := maps.Clone(env) // variables and temporaries share one register map
	pc, res := 0, ""
	steps := map[tac.Op]func(tac.Instr){
		tac.OpConst:  func(x tac.Instr) { regs[x.Result] = parseLit(x.A) },
		tac.OpCopy:   func(x tac.Instr) { regs[x.Result] = regs[x.A] },
		tac.OpBinary: func(x tac.Instr) { regs[x.Result] = binFns[x.BinOp](regs[x.A], regs[x.B]) },
		tac.OpNeg:    func(x tac.Instr) { regs[x.Result] = -regs[x.A].(int64) },
		tac.OpNot:    func(x tac.Instr) { regs[x.Result] = !truthy(regs[x.A]) },
		tac.OpGoto:   func(x tac.Instr) { pc = labels[x.Label] },
	}
	for ; pc < len(is); pc++ {
		x := is[pc]
		if x.Op == tac.OpJmpFalse || x.Op == tac.OpJmpTrue {
			t := truthy(regs[x.A])
			if t == (x.Op == tac.OpJmpTrue) {
				pc = labels[x.Label]
			}
		} else if f := steps[x.Op]; f != nil {
			f(x)
		}
		if x.Result != "" {
			res = x.Result
		}
	}
	return regs[res]
}

// SelfCheck verifies the four invariants on a set of built-in ASTs.
func SelfCheck() error {
	a, b, c := ast.V("a"), ast.V("b"), ast.V("c")
	cases := []*ast.Expr{
		ast.Bin("||", ast.Bin("&&", a, b), c),
		ast.Bin("-", ast.Bin("-", a, b), c),
		ast.Bin("+", ast.IfElse(c, ast.Int(1), ast.Int(2)), ast.Int(3)),
		ast.Bin("&&", ast.Bool(false), ast.Bin(">", ast.Bin("/", ast.Int(1), ast.Int(0)), ast.Int(0))),
		ast.Not(ast.Neg(a)),
	}
	env := map[string]any{"a": int64(0), "b": int64(3), "c": int64(2)}
	for i, n := range cases {
		is, err := tac.Gen(n)
		if err != nil {
			return fmt.Errorf("case %d: %w", i, err)
		}
		if e := checkStream(is); e != nil {
			return fmt.Errorf("case %d: %w", i, e)
		}
		if got, want := runTAC(is, env), evalAST(n, env); got != want {
			return fmt.Errorf("case %d: tac=%v ast=%v", i, got, want)
		}
	}
	if is, err := tac.Gen(nil); err == nil || is != nil {
		return fmt.Errorf("nil node must be rejected without output")
	}
	return nil
}

func checkStream(is []tac.Instr) error {
	next, seen := 1, map[string]bool{}
	lastDef, res, lastWrite := map[string]int{}, "", -1
	for k, x := range is {
		r := x.Result
		if r == "" {
			continue
		}
		if !seen[r] {
			seen[r] = true
			if r != fmt.Sprintf("t%d", next) {
				return fmt.Errorf("temp gap at %s", r)
			}
			next++
		}
		lastDef[r], res, lastWrite = k, r, k
	}
	if lastDef[res] != lastWrite {
		return fmt.Errorf("result temp %s rewritten after final point", res)
	}
	return nil
}
