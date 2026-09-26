package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"ontology/api"
	"ontology/ast"
	"ontology/tac"
)

var failed bool

func check(name string, ok bool, detail ...string) {
	if !ok {
		failed = true
	}
	fmt.Println(map[bool]string{true: "OK", false: "FAIL"}[ok], name, strings.Join(detail, " "))
}

func render(is []tac.Instr) string {
	ss := make([]string, len(is))
	for i, x := range is {
		ss[i] = x.String()
	}
	return strings.Join(ss, " ; ")
}

// peakLive recomputes max simultaneously live result temps from the stream.
func peakLive(is []tac.Instr) int {
	live, peak := map[string]bool{}, 0
	use := func(o string) {
		if strings.HasPrefix(o, "t") {
			delete(live, o)
		}
	}
	for _, x := range is {
		if x.Op == tac.OpBinary || x.Op == tac.OpNeg || x.Op == tac.OpNot {
			use(x.B)
		}
		if x.Op != tac.OpJmpFalse && x.Op != tac.OpJmpTrue && x.Result != "" {
			use(x.A)
			live[x.Result] = true
		}
		if len(live) > peak {
			peak = len(live)
		}
	}
	return peak
}

func main() {
	a, b, c := ast.V("a"), ast.V("b"), ast.V("c")
	is, err := tac.Gen(ast.Bin("||", ast.Bin("&&", a, b), c))
	want := "t1 = a|if t1 == 0 goto L1|t2 = b|t1 = t2|L1:|if t1 != 0 goto L2|t3 = c|t1 = t3|L2:"
	got := []string{}
	for _, x := range is {
		got = append(got, x.String())
	}
	check("andor", err == nil && strings.Join(got, "|") == want, render(is))
	dz := ast.Bin("&&", ast.Bool(false), ast.Bin(">", ast.Bin("/", ast.Int(1), ast.Int(0)), ast.Int(0)))
	is, _ = tac.Gen(dz)
	divInSkipped := false
	if len(is) >= 2 && is[1].Op == tac.OpJmpFalse {
		for _, x := range is[2:] {
			if x.Op == tac.OpLabel {
				break
			}
			if x.Op == tac.OpBinary && x.BinOp == "/" {
				divInSkipped = true // jump lands past it: never evaluated
			}
		}
	}
	check("short-circuit-1/0-unreachable", divInSkipped, render(is))
	is, _ = tac.Gen(ast.Bin("-", ast.Bin("-", a, b), c))
	check("a-b-c-left-assoc", len(is) == 5 && is[2].BinOp == "-" && is[4].BinOp == "-" &&
		is[4].A == is[2].Result, render(is))
	is, _ = tac.Gen(ast.Bin("+", ast.IfElse(c, ast.Int(1), ast.Int(2)), ast.Int(3)))
	var joins []tac.Instr
	var add tac.Instr
	for _, x := range is {
		if x.Op == tac.OpCopy && strings.HasPrefix(x.Result, "t") {
			joins = append(joins, x)
		}
		if x.Op == tac.OpBinary && x.BinOp == "+" {
			add = x
		}
	}
	check("if-else-join-temp", len(joins) >= 2 &&
		joins[len(joins)-2].Result == joins[len(joins)-1].Result &&
		add.A == joins[len(joins)-1].Result, render(is))
	is, _ = tac.Gen(ast.Bin("+", ast.V("x"), ast.Bin("*", ast.V("y"), ast.V("z"))))
	check("x+y*z", is[3].BinOp == "*" && is[4].BinOp == "+" && is[4].B == is[3].Result, render(is))
	_, e1 := tac.Gen(nil)
	_, e2 := tac.Gen(ast.Bin("%", ast.Int(1), ast.Int(2)))
	_, e3 := tac.Gen(ast.IfElse(ast.Bool(true), nil, ast.Int(2)))
	check("3-distinct-errors", errors.Is(e1, tac.ErrNilNode) &&
		errors.Is(e2, tac.ErrUnknownOp) && errors.Is(e3, tac.ErrMissingBranch),
		e1.Error(), "|", e2.Error(), "|", e3.Error())
	is, after := tac.Gen(ast.Int(7))
	check("state-clean-after-reject", after == nil && len(is) == 1 && is[0].Result == "t1")
	chain := ast.V("v0")
	for i := 1; i < 10000; i++ {
		chain = ast.Bin("+", chain, ast.V(fmt.Sprintf("v%d", i)))
	}
	is, _ = tac.Gen(chain)
	check("peak-live-const-m=10000", peakLive(is) <= 3, fmt.Sprintf("peak=%d instrs=%d", peakLive(is), len(is)))
	check("selfcheck", api.SelfCheck() == nil)
	trees := []*ast.Expr{
		ast.Bin("||", ast.Bin("&&", a, b), c),
		ast.IfElse(ast.Bin("<", a, b), ast.Bin("*", b, c), ast.Neg(c)),
		ast.Bin("==", ast.Not(a), ast.Bin(">=", b, c)),
	}
	N := 32
	all := make([][]string, N)
	var wg sync.WaitGroup
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for _, tr := range trees {
				ii, err := api.GenExpr(tr)
				if err != nil {
					panic(err)
				}
				for _, x := range ii {
					all[g] = append(all[g], x.String())
				}
			}
		}(g)
	}
	wg.Wait()
	concurOK := true
	for g := 1; g < N && concurOK; g++ {
		concurOK = strings.Join(all[g], "|") == strings.Join(all[0], "|")
	}
	check("concurrent-gen-identical", concurOK, fmt.Sprintf("goroutines=%d", N))
	if failed {
		panic("demo failed")
	}
}
