package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"

	"ontology/api"
	"ontology/ast"
	"ontology/tac"
)

var failed bool

func check(ok bool, msg string) {
	if !ok {
		failed = true
		fmt.Println("FAIL " + msg)
		return
	}
	fmt.Println("OK " + msg)
}

func str(in tac.Instr) string {
	switch in.Op {
	case tac.OpConstInt:
		return fmt.Sprintf("%s = %d", in.Dst, in.Imm)
	case tac.OpConstBool:
		return fmt.Sprintf("%s = %v", in.Dst, in.ImmB)
	case tac.OpCopy:
		return in.Dst + " = " + in.A
	case tac.OpBin:
		return fmt.Sprintf("%s = %s %s %s", in.Dst, in.A, in.BinOp, in.B)
	case tac.OpNeg:
		return in.Dst + " = -" + in.A
	case tac.OpNot:
		return in.Dst + " = !" + in.A
	case tac.OpIfFalse:
		return "if " + in.A + " == 0 goto " + in.Label
	case tac.OpIfTrue:
		return "if " + in.A + " != 0 goto " + in.Label
	case tac.OpGoto:
		return "goto " + in.Label
	default:
		return in.Label + ":"
	}
}
func join(code []tac.Instr) string {
	var b strings.Builder
	for i, in := range code {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(str(in))
	}
	return b.String()
}
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

func main() {
	e := ast.Bin("+", ast.Var("x"), ast.Bin("*", ast.Var("y"), ast.Var("z")))
	check(e.Kind == ast.KBin && e.R.Op == "*", "ast: build x + y * z")

	abc := ast.Logic("||", ast.Logic("&&", ast.Var("a"), ast.Var("b")), ast.Var("c"))
	code, err := tac.Gen(abc)
	want := "t1 = a; if t1 == 0 goto L1; t2 = b; t1 = t2; L1:; " +
		"if t1 != 0 goto L2; t3 = c; t1 = t3; L2:"
	check(err == nil && join(code) == want, "(a&&b)||c: "+join(code))

	check(api.SelfCheck() == nil, "api.SelfCheck: invariants 1-4 on built-in set")

	div := ast.Bin(">", ast.Bin("/", ast.Int(1), ast.Int(0)), ast.Int(0))
	sc, _ := api.GenExpr(ast.Logic("&&", ast.Bool(false), div))
	v, err := tac.Exec(sc, nil)
	check(err == nil && v == 0, fmt.Sprintf("false&&(1/0>0): %d instrs, exec=0, no div evaluated", len(sc)))

	subE := ast.Bin("-", ast.Bin("-", ast.Var("a"), ast.Var("b")), ast.Var("c"))
	sub, _ := api.GenExpr(subE)
	check(join(sub) == "t1 = a - b; t2 = t1 - c", "a-b-c: "+join(sub))

	ifE := ast.Bin("+", ast.If(ast.Var("c"), ast.Int(1), ast.Int(2)), ast.Int(3))
	ife, _ := api.GenExpr(ifE)
	want = "t1 = c; if t1 == 0 goto L1; t2 = 1; t3 = t2; goto L2; L1:; " +
		"t4 = 2; t3 = t4; L2:; t5 = 3; t6 = t3 + t5"
	check(join(ife) == want, "(if c then 1 else 2)+3: merge temp t3, +3 reads t3")

	xyz, _ := api.GenExpr(e)
	check(join(xyz) == "t1 = y * z; t2 = x + t1", "x+y*z: "+join(xyz))

	_, errN := api.GenExpr(nil)
	_, errU := api.GenExpr(ast.Bin("%", ast.Int(1), ast.Int(2)))
	_, errM := api.GenExpr(ast.If(ast.Bool(true), ast.Int(1), nil))
	ok := errors.Is(errN, tac.ErrNilNode) && errors.Is(errU, tac.ErrUnknownOp) &&
		errors.Is(errM, tac.ErrMissingBranch) &&
		!errors.Is(errN, errU) && !errors.Is(errU, errM) && !errors.Is(errN, errM)
	again, _ := api.GenExpr(ast.Var("v"))
	check(ok && len(again) == 1 && again[0].Dst == "t1", "errors: 3 distinct sentinels, no trace after reject")

	chain := ast.Var("v")
	for i := 0; i < 9999; i++ {
		chain = ast.Bin("-", chain, ast.Var("v"))
	}
	cc, _ := api.GenExpr(chain)
	check(livePeak(cc) <= 2, fmt.Sprintf("live peak <= 2 for m=10000 chain (%d instrs)", len(cc)))

	exprs := []*ast.Expr{abc, subE, ifE, e}
	got := make([][][]tac.Instr, 8)
	var wg sync.WaitGroup
	for g := range got {
		got[g] = make([][]tac.Instr, len(exprs))
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i, e := range exprs {
				got[g][i], _ = api.GenExpr(e)
			}
		}(g)
	}
	wg.Wait()
	same := true
	for g := 1; g < len(got); g++ {
		for i := range got[g] {
			same = same && slices.Equal(got[g][i], got[0][i])
		}
	}
	check(same, "concurrent: 8 goroutines x 4 ASTs identical")

	if failed {
		os.Exit(1)
	}
}
