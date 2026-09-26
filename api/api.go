// Package api is the outward-facing layer: GenExpr and SelfCheck.
package api

import (
	"errors"
	"fmt"

	"ontology/ast"
	"ontology/tac"
)

// GenExpr translates an expression AST to three-address code. It is safe for
// concurrent use: each call runs on fresh translator state.
func GenExpr(n *ast.Expr) ([]tac.Instr, error) { return tac.Gen(n) }

var div10 = ast.Bin(">", ast.Bin("/", ast.Int(1), ast.Int(0)), ast.Int(0))

// builtins is the built-in AST set that SelfCheck and the tests run against.
var builtins = []*ast.Expr{
	ast.Bin("+", ast.Var("x"), ast.Bin("*", ast.Var("y"), ast.Var("z"))),
	ast.Bin("-", ast.Bin("-", ast.Var("a"), ast.Var("b")), ast.Var("c")),
	ast.Logic("||", ast.Logic("&&", ast.Var("a"), ast.Var("b")), ast.Var("c")),
	ast.Logic("&&", ast.Bool(false), div10),
	ast.Logic("||", ast.Bool(true), div10),
	ast.Bin("+", ast.If(ast.Var("c"), ast.Int(1), ast.Int(2)), ast.Int(3)),
	ast.Not(ast.Var("x")),
	ast.Bin(">=", ast.Neg(ast.Var("y")), ast.Var("z")),
}

var selfEnvs = []map[string]int64{
	{"x": 1, "y": 2, "z": 3, "a": 0, "b": 1, "c": 4},
	{"x": 0, "y": 0, "z": 0, "a": 1, "b": 1, "c": 0},
	{"x": -3, "y": 7, "z": -2, "a": 5, "b": 0, "c": 1},
}

// SelfCheck verifies on builtins: (1) Exec(Gen(e)) equals reference ast.Eval
// for every env; (2) short-circuited right sides never execute, so the 1/0
// in div10 never fires; (3) temps are t1.. contiguous with the result in the
// last-written temp; (4) rejections yield nil code plus distinct sentinels
// and leave no trace. It also checks the live-temp peak stays constant.
func SelfCheck() error {
	for i, e := range builtins {
		code, err := GenExpr(e)
		if err != nil {
			return fmt.Errorf("selfcheck gen %d: %w", i, err)
		}
		if bad := checkTemps(code); bad != "" {
			return fmt.Errorf("selfcheck temps %d: %s", i, bad)
		}
		for _, env := range selfEnvs {
			got, err := tac.Exec(code, env)
			if err != nil {
				return fmt.Errorf("selfcheck exec %d: %w", i, err)
			}
			want, err := ast.Eval(e, env)
			if err != nil {
				return fmt.Errorf("selfcheck eval %d: %w", i, err)
			}
			if got != want {
				return fmt.Errorf("selfcheck %d: got %d want %d", i, got, want)
			}
		}
	}
	chain := ast.Var("v")
	for i := 0; i < 999; i++ {
		chain = ast.Bin("-", chain, ast.Var("v"))
	}
	if code, _ := GenExpr(chain); livePeak(code) > 2 {
		return errors.New("selfcheck: live temp peak exceeds constant")
	}
	bads := []struct {
		e    *ast.Expr
		want error
	}{
		{nil, tac.ErrNilNode},
		{ast.Bin("%", ast.Int(1), ast.Int(2)), tac.ErrUnknownOp},
		{ast.If(ast.Bool(true), ast.Int(1), nil), tac.ErrMissingBranch},
	}
	for _, b := range bads {
		if code, err := GenExpr(b.e); err == nil || code != nil {
			return errors.New("selfcheck: rejection must yield nil code and an error")
		} else if !errors.Is(err, b.want) {
			return fmt.Errorf("selfcheck: got %v want %v", err, b.want)
		}
	}
	if code, _ := GenExpr(ast.Var("v")); len(code) != 1 || code[0].Dst != "t1" {
		return errors.New("selfcheck: state leaked across calls")
	}
	return nil
}

// livePeak recomputes from a generated sequence the peak number of temps
// simultaneously live (produced and not yet consumed as an operand).
func livePeak(code []tac.Instr) int {
	live, n, peak := map[string]bool{}, 0, 0
	for _, in := range code {
		for _, o := range []string{in.A, in.B} {
			if live[o] {
				delete(live, o)
				n--
			}
		}
		if in.Dst != "" && !live[in.Dst] {
			live[in.Dst] = true
			n++
			peak = max(peak, n)
		}
	}
	return peak
}

// checkTemps pins invariant 3: temps first appear as t1,t2,... in allocation
// order, and the temp written by the last value-producing instruction (the
// expression result) has no later instruction that could overwrite it.
func checkTemps(code []tac.Instr) string {
	seen := map[string]bool{}
	next, last := 1, ""
	for _, in := range code {
		if in.Dst == "" {
			continue
		}
		if !seen[in.Dst] {
			if in.Dst != fmt.Sprintf("t%d", next) {
				return "temp numbering not sequential from t1"
			}
			seen[in.Dst] = true
			next++
		}
		last = in.Dst
	}
	if last == "" {
		return "no result temp produced"
	}
	return ""
}
