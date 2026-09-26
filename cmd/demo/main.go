// demo 顺序演示 lex/parse/api 的判定，全部 OK 且退出码 0 才算交付。
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"ontology/api"
	"ontology/lex"
	"ontology/parse"
)

var failed bool

func check(name string, cond bool) {
	if cond {
		fmt.Println("OK  " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
	}
}

func mustLex(s string) []lex.Token {
	t, err := lex.Lex(s)
	if err != nil {
		panic(err)
	}
	return t
}

func main() {
	t1, e1 := lex.Lex("12+3*(4-5)")
	_, e2 := lex.Lex("1@2")
	_, e3 := lex.Lex("99999999999999999999")
	check("lex: tokens+values, illegal char & int64 overflow rejected",
		e1 == nil && len(t1) == 9 && t1[0].Val == 12 && t1[3].Kind == lex.Mul &&
			errors.Is(e2, lex.ErrLexical) && errors.Is(e3, lex.ErrLexical))

	n, err := parse.Parse(mustLex("8/2/2-3"))
	shape := err == nil && n.Op == parse.Sub && n.Left.Op == parse.Div && n.Left.Left.Op == parse.Div
	_, p1 := parse.Parse(mustLex("(1"))
	_, p2 := parse.Parse(mustLex("1)"))
	_, p3 := parse.Parse(mustLex("1 2"))
	_, p4 := parse.Parse(nil)
	check("parse: left-assoc AST, paren/trailing/empty rejected",
		shape && errors.Is(p1, parse.ErrParen) && errors.Is(p2, parse.ErrParen) &&
			errors.Is(p3, parse.ErrTrailing) && errors.Is(p4, parse.ErrOperand))

	n, _ = api.Parse("8/2/2-3")
	var steps []string
	var walk func(x *parse.Node)
	walk = func(x *parse.Node) {
		if x.Left != nil {
			walk(x.Left)
		}
		if x.Right != nil {
			walk(x.Right)
		}
		v, _ := api.Eval(x)
		steps = append(steps, fmt.Sprint(v))
	}
	walk(n)
	check("post-order node values of 8/2/2-3: "+strings.Join(steps, " "),
		strings.Join(steps, " ") == "8 2 4 2 2 3 -1")

	exprs := map[string]int64{"8/2/2-3": -1, "2+3*4": 14, "-7/2": -3, "8-3-2": 3, "- -3": 3, "(1+2)*3": 9}
	ok := true
	for s, want := range exprs {
		v, err := api.ParseAndEval(s)
		ok = ok && err == nil && v == want
	}
	check("eval: 8/2/2-3=-1, 2+3*4=14, -7/2=-3, 8-3-2=3, - -3=3, (1+2)*3=9", ok)

	errOf := func(s string) error { _, err := api.ParseAndEval(s); return err }
	cats := []error{lex.ErrLexical, parse.ErrParen, api.ErrDivZero, parse.ErrOperand, parse.ErrTrailing}
	got := []error{errOf("1@2"), errOf("(1"), errOf("1/0"), errOf(""), errOf("1 2")}
	ok = true
	for i, g := range got {
		for j, c := range cats {
			ok = ok && errors.Is(g, c) == (i == j)
		}
	}
	check("errors: lexical/paren/div-zero/empty/trailing all distinct", ok)

	for _, s := range []string{"1@2", "(1", "1/0", "", "1 2"} {
		_, _ = api.ParseAndEval(s)
	}
	v14, err := api.ParseAndEval("2+3*4")
	check("no state left after failures", err == nil && v14 == 14)

	_, err = api.Parse("1" + strings.Repeat("+1", 9999))
	check("lookahead peak <= 1 at m=10000 (asserted by parse.TestLookaheadPeak)", err == nil)

	want := make([]int64, 0, len(exprs))
	list := []string{"8/2/2-3", "2+3*4", "-7/2", "8-3-2", "- -3", "(1+2)*3"}
	for _, s := range list {
		v, _ := api.ParseAndEval(s)
		want = append(want, v)
	}
	results := make([][]int64, 32)
	var wg sync.WaitGroup
	for g := range results {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for _, s := range list {
				v, err := api.ParseAndEval(s)
				if err == nil {
					results[g] = append(results[g], v)
				}
			}
		}(g)
	}
	wg.Wait()
	ok = len(results[0]) == len(want)
	for _, r := range results {
		for i := range want {
			ok = ok && len(r) == len(want) && r[i] == want[i]
		}
	}
	check("concurrent ParseAndEval identical across 32 goroutines", ok)

	check("api.SelfCheck", api.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
