// Command demo 逐条演示重写器的正确性判定，全部通过时退出码为 0。
package main

import (
	"fmt"
	"math/rand"

	"ontology/ast"
	"ontology/equiv"
	"ontology/fold"
	"ontology/push"
	"ontology/rules"
)

var failed int

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed++
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	check("ast: canonical print is order-insensitive",
		ast.AndN(ast.Col("t", "b"), ast.Col("t", "a")).String() ==
			ast.AndN(ast.Col("t", "a"), ast.Col("t", "b")).String())
	a, b := ast.Col("t", "a"), ast.Col("t", "b")
	check("equiv: de-morgan NOT(A AND B) == NOT A OR NOT B on 3^2",
		equiv.Check(ast.Not1(ast.AndN(a, b)),
			ast.OrN(ast.Not1(a), ast.Not1(b)), nil) == nil)
	x := ast.Col("t", "x")
	check("fold: x=x is NOT folded to TRUE",
		fold.Fold(ast.Eq2(x, x)).Kind == ast.Eq)
	p := ast.Col("t", "p")
	check("fold: A OR NOT A is NOT folded to TRUE",
		fold.Fold(ast.OrN(p, ast.Not1(p))).Kind == ast.Or)
	check("fold: A AND NOT A folded at top but kept under NOT",
		fold.Fold(ast.AndN(p, ast.Not1(p))).String() == "FALSE" &&
			fold.Fold(ast.Not1(ast.AndN(p, ast.Not1(p)))).Kind == ast.Not)
	cols := []string{"t.a", "t.b", "t.c"}
	rng := rand.New(rand.NewSource(42))
	equivFails, maxRounds, maxBound := 0, 0, 0
	for i := 0; i < 200; i++ {
		tree := rules.GenRandom(rng, cols, 5)
		out, st, err := rules.Rewrite(tree, rules.Default())
		if err != nil || equiv.Check(tree, out, cols) != nil {
			equivFails++
		}
		if st.Rounds > maxRounds {
			maxRounds, maxBound = st.Rounds, 4*tree.Size()
		}
	}
	check("rules: 200 random trees equivalent after rewrite", equivFails == 0)
	check(fmt.Sprintf("rules: max rounds %d <= bound %d", maxRounds, maxBound),
		maxRounds <= maxBound)
	inverse := append(rules.Default(), rules.Rule{
		Name: "neg-intro",
		Local: func(n *ast.Node) (*ast.Node, bool) {
			if n.Kind == ast.Column {
				return ast.Not1(ast.Not1(n)), true
			}
			return nil, false
		},
	})
	_, _, oscErr := rules.Rewrite(ast.Col("t", "a"), inverse)
	check("rules: oscillation from inverse rules detected",
		oscErr == rules.ErrOscillation)
	base := ast.Not1(ast.AndN(ast.Col("t", "a"),
		ast.OrN(ast.Col("t", "b"), ast.K(ast.Unknown))))
	want, _, _ := rules.Rewrite(base, rules.Default())
	deterministic := true
	for i := 0; i < 20; i++ {
		rs := rules.Default()
		rand.New(rand.NewSource(int64(i))).Shuffle(len(rs),
			func(x, y int) { rs[x], rs[y] = rs[y], rs[x] })
		got, _, err := rules.Rewrite(base, rs)
		if err != nil || got.String() != want.String() {
			deterministic = false
		}
	}
	check("rules: shuffled rule order x20 gives identical output", deterministic)
	plan := &push.Plan{
		Where: ast.AndN(ast.Col("o", "a"), ast.Col("c", "b"),
			ast.Eq2(ast.Col("o", "id"), ast.Col("c", "oid"))),
		Scans: []*push.Scan{{Table: "o"}, {Table: "c"}},
	}
	pushOK := push.Push(plan) == nil && push.Check(plan) == nil &&
		plan.Scans[0].Pred != nil && plan.Scans[1].Pred != nil
	check("push: single-table predicates pushed, no cross-table refs", pushOK)
	fmt.Printf("TOTAL %d failed\n", failed)
	if failed > 0 {
		panic("demo failed")
	}
}
