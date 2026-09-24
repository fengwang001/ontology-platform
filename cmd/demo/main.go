// Command demo exercises the attribute-level permission filter end to end.
package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/filter"
	"ontology/policy"
	"ontology/predicate"
	"ontology/report"
)

var failures int

func ok(name string, cond bool) {
	if cond {
		fmt.Printf("OK   %s\n", name)
		return
	}
	fmt.Printf("FAIL %s\n", name)
	failures++
}

func and(k ...predicate.Node) predicate.Node { return predicate.And{Children: k} }
func or(k ...predicate.Node) predicate.Node  { return predicate.Or{Children: k} }
func not(c predicate.Node) predicate.Node    { return predicate.Not{Child: c} }
func eq(c string) predicate.Node             { return predicate.Compare{Column: c, Value: 1} }

func main() {
	pol := policy.New()
	pol.Grant("analyst", "id", "name")
	pol.Declare("secret")
	vis := pol.Visible("analyst")
	ok("policy: grants visible, secret known but invisible",
		vis["id"] && vis["name"] && !vis["secret"] &&
			pol.Known("secret") && !pol.Known("ghost") &&
			len(pol.Visible("nobody")) == 0)

	tree := and(eq("id"), or(eq("name"), not(predicate.IsNull{Column: "name"})))
	ok("predicate: node count of sample tree is 6", predicate.Count(tree) == 6)

	same := true
	for i := 1; i < 20; i++ {
		same = same && shuffledReport(i) == shuffledReport(0)
	}
	ok("report: byte-identical across 20 shuffles", same)

	eng := filter.NewEngine(pol)

	errNot := eng.Check("analyst", not(eq("secret")))
	var refErr *filter.RefError
	ok("filter: NOT(secret=1) rejected, names secret@$.not",
		errors.Is(errNot, filter.ErrInvisibleColumn) &&
			errors.As(errNot, &refErr) && len(refErr.Refs) == 1 &&
			refErr.Refs[0].Column == "secret" && refErr.Refs[0].Path == "$.not")

	ok("filter: secret IS NULL rejected, no rows returned",
		errors.Is(eng.Check("analyst", predicate.IsNull{Column: "secret"}), filter.ErrInvisibleColumn))

	rows := []map[string]any{{"id": 7, "name": "ada", "secret": 1}}
	_, pruned, errExec := eng.Execute("analyst", eq("id"), rows)
	_, hasSecret := pruned[0]["secret"]
	_, zeroHas := map[string]any{"secret": nil}["secret"]
	ok("filter: pruned row drops secret, differs from zero-fill",
		errExec == nil && len(pruned) == 1 && len(pruned[0]) == 2 &&
			!hasSecret && zeroHas)

	eng.ResetStats()
	_ = eng.Check("analyst", tree)
	ok("filter: visited nodes == tree node count",
		eng.Stats().VisitedNodes == predicate.Count(tree))

	wide := filter.NewEngine(widePolicy())
	bigRow := map[string]any{}
	for i := 0; i < 1000; i++ {
		bigRow[fmt.Sprintf("c%d", i)] = i
	}
	wide.Prune("r", bigRow)
	copies := wide.Stats().CopiedCells
	ok("filter: 1000 cols / 5 visible, copies 5 <= 4*5", copies == 5 && copies <= 4*5)

	ok("filter: 32 exhaustive combos match definition", exhaustive())

	errEmpty := eng.Check("nobody", eq("id"))
	prunedEmpty := eng.Prune("nobody", rows[0])
	ok("filter: empty visible set rejects and prunes to empty row",
		errors.Is(errEmpty, filter.ErrInvisibleColumn) && len(prunedEmpty) == 0)

	pol.Grant("full", "id", "name", "secret")
	errFull := eng.Check("full", not(eq("secret")))
	prunedFull := eng.Prune("full", rows[0])
	ok("filter: full visible set allows and keeps all columns",
		errFull == nil && len(prunedFull) == 3)

	tru, fls := predicate.Const{Value: true}, predicate.Const{Value: false}
	ok("filter: short-circuited invisible refs allowed, evaluated ones rejected",
		eng.Check("analyst", or(tru, eq("secret"))) == nil &&
			eng.Check("analyst", and(fls, eq("secret"))) == nil &&
			errors.Is(eng.Check("analyst", or(fls, eq("secret"))), filter.ErrInvisibleColumn))

	fmt.Printf("TOTAL %d checks, %d failures\n", 12, failures)
	if failures > 0 {
		os.Exit(1)
	}
}

func widePolicy() *policy.Policy {
	p := policy.New()
	p.Grant("r", "c0", "c1", "c2", "c3", "c4")
	return p
}

// exhaustive checks all 8 visibility combos of columns a,b,c against
// the 4 predicate shapes (=, NOT, AND, OR), per the DESIGN.md rule:
// reject iff any referenced column is invisible (no constants here, so
// no folding applies).
func exhaustive() bool {
	cols := []string{"a", "b", "c"}
	all := []predicate.Node{eq("a"), eq("b"), eq("c")}
	shapes := []func() predicate.Node{
		func() predicate.Node { return eq("a") },
		func() predicate.Node { return not(eq("a")) },
		func() predicate.Node { return predicate.And{Children: all} },
		func() predicate.Node { return predicate.Or{Children: all} },
	}
	for mask := 0; mask < 8; mask++ {
		p := policy.New()
		vis := map[string]bool{}
		for i, c := range cols {
			p.Declare(c)
			if mask&(1<<i) != 0 {
				p.Grant("r", c)
				vis[c] = true
			}
		}
		eng := filter.NewEngine(p)
		for shape, mk := range shapes {
			err := eng.Check("r", mk())
			referenced := cols[:1]
			if shape >= 2 {
				referenced = cols
			}
			want := map[string]bool{} // invisible referenced columns
			for _, c := range referenced {
				if !vis[c] {
					want[c] = true
				}
			}
			if (err == nil) != (len(want) == 0) {
				return false
			}
			if err == nil {
				continue
			}
			var re *filter.RefError
			if !errors.As(err, &re) {
				return false
			}
			named := map[string]bool{}
			for _, ref := range re.Refs {
				named[ref.Column] = true
			}
			if len(named) != len(want) {
				return false
			}
			for c := range want {
				if !named[c] {
					return false
				}
			}
		}
	}
	return true
}

// shuffledReport builds the same logical report with field insertion
// order permuted by seed, and renders it.
func shuffledReport(seed int) string {
	cols := permute([]string{"city", "salary", "secret"}, seed)
	refs := permute([]report.Ref{
		{Column: "secret", Path: "$.and[0]"},
		{Column: "salary", Path: "$.and[1].not"},
		{Column: "secret", Path: "$.and[2]"},
	}, seed)
	rep := report.Report{Rejected: true, Dropped: cols, Refs: refs}
	rep.Normalize()
	return rep.String()
}

func permute[T any](xs []T, seed int) []T {
	out := append([]T(nil), xs...)
	for i := range out {
		j := (seed + i*i + 1) % len(out)
		out[i], out[j] = out[j], out[i]
	}
	return out
}
