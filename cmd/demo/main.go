// Command demo runs the acceptance checks for the predicate rewriter.
package main

import (
	"fmt"
	"math/rand"
	"os"
	"strings"

	"ontology/ast"
	"ontology/equiv"
	"ontology/push"
	"ontology/rules"
)

var failures int

func report(ok bool, label string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", status, label)
}

func rewrite(n *ast.Node) (*ast.Node, rules.Stats) {
	out, st, err := rules.Fixpoint(n, rules.Std())
	if err != nil {
		panic(err)
	}
	return out, st
}

func checkDeMorgan() {
	a, b := ast.Col("t", "a"), ast.Col("t", "b")
	eq, ce := equiv.Equal(ast.Not(ast.And(a, b)), ast.Or(ast.Not(a), ast.Not(b)))
	report(eq, "de-morgan NOT(A AND B) == NOT A OR NOT B "+ce)
}

func checkNonFoldings() {
	x := ast.Col("t", "x")
	out, _ := rewrite(ast.Eq(x, x))
	report(out.String() != "true", "x=x not folded to true (got "+out.String()+")")
	a := ast.Col("t", "a")
	out, _ = rewrite(ast.Or(a, ast.Not(a)))
	report(out.String() != "true", "A OR NOT A not folded to true (got "+out.String()+")")
}

func checkPosition() {
	x := ast.Col("t", "x")
	top, _ := rewrite(ast.And(x, ast.Not(x)))
	under, _ := rewrite(ast.Not(ast.And(x, ast.Not(x))))
	report(top.String() == "false" && under.String() == "(or (not t.x) t.x)",
		"A AND NOT A folded at top, kept under NOT ("+top.String()+" vs "+under.String()+")")
}

func checkRandom() {
	r := rand.New(rand.NewSource(1))
	cols := []string{"t1.a", "t1.b", "t2.c", "t2.d"}
	fails, boundFails, sumRounds, sumDelta := 0, 0, 0, 0
	for i := 0; i < 200; i++ {
		tree := rules.Random(r, cols, 5)
		out, st, err := rules.Fixpoint(tree, rules.Std())
		if err != nil {
			fails++
			continue
		}
		if eq, _ := equiv.FilterEqual(tree, out); !eq {
			fails++
		}
		if st.Rounds > 4*ast.Size(tree) || st.Visits > 4*st.Rounds*st.MaxSize {
			boundFails++
		}
		sumRounds += st.Rounds
		sumDelta += ast.Size(out) - ast.Size(tree)
	}
	report(fails == 0, fmt.Sprintf("200 random trees equivalent, failures=%d, avg nodes %+.2f, avg rounds %.2f",
		fails, float64(sumDelta)/200, float64(sumRounds)/200))
	report(boundFails == 0, fmt.Sprintf("rounds<=4*nodes and visits bound, violations=%d", boundFails))
}

func checkOscillation() {
	inverse := []rules.Rule{
		{Name: "x-to-y", Apply: func(n *ast.Node) (*ast.Node, bool) {
			if n.Kind == ast.KColumn && n.Name == "x" {
				return ast.Col("t", "y"), true
			}
			return n, false
		}},
		{Name: "y-to-x", Apply: func(n *ast.Node) (*ast.Node, bool) {
			if n.Kind == ast.KColumn && n.Name == "y" {
				return ast.Col("t", "x"), true
			}
			return n, false
		}},
	}
	_, _, err := rules.Fixpoint(ast.Col("t", "x"), inverse)
	report(err != nil && strings.Contains(err.Error(), "oscillation"), "oscillation detected: "+fmt.Sprint(err))
}

func checkDeterminism() {
	x, y := ast.Col("t", "x"), ast.Col("t", "y")
	tree := ast.Not(ast.And(ast.Or(x, ast.Const(ast.False)), ast.Not(ast.Not(y)), x))
	want, _ := rewrite(tree)
	same := true
	r := rand.New(rand.NewSource(7))
	for i := 0; i < 20; i++ {
		rs := rules.Std()
		r.Shuffle(len(rs), func(a, b int) { rs[a], rs[b] = rs[b], rs[a] })
		got, _, _ := rules.Fixpoint(tree, rs)
		if got.String() != want.String() {
			same = false
		}
	}
	report(same, "shuffled rule order x20 gives identical output: "+want.String())
}

func checkPush() {
	plan := push.Join(push.Scan("t1"), push.Join(push.Scan("t2"), push.Scan("t3")))
	pred := ast.And(
		ast.Eq(ast.Col("t1", "a"), ast.Const(ast.True)),
		ast.Ne(ast.Col("t2", "c"), ast.Col("t2", "d")),
		ast.Eq(ast.Col("t1", "b"), ast.Col("t3", "e")),
	)
	err := push.Push(plan, pred)
	report(err == nil && push.Check(plan) == nil, "push-down leaves no cross-table refs: "+plan.String())
	bad := push.Join(push.Scan("t1"), push.Scan("t2"))
	err = push.Push(bad, ast.Eq(ast.Col("t9", "z"), ast.Const(ast.True)))
	report(err != nil, "unknown table is a decidable error: "+fmt.Sprint(err))
}

func main() {
	checkNonFoldings()
	checkDeMorgan()
	checkPosition()
	checkRandom()
	checkOscillation()
	checkPush()
	checkDeterminism()
	fmt.Printf("TOTAL %d failure(s)\n", failures)
	if failures > 0 {
		os.Exit(1)
	}
}
