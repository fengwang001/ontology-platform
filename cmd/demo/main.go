// Command demo exercises the attribute-level permission filter end to end.
package main

import (
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"os"

	"ontology/filter"
	"ontology/policy"
	"ontology/predicate"
	"ontology/report"
)

var passed, failed int

func check(name string, ok bool) {
	if ok {
		passed++
		fmt.Println("OK   " + name)
		return
	}
	failed++
	fmt.Println("FAIL " + name)
}

func main() {
	pol := policy.New()
	pol.Grant("analyst", "id", "name", "dept")
	pol.Grant("auditor")
	vis, err := pol.Visible("analyst")
	check("policy: analyst sees exactly {id,name,dept}",
		err == nil && len(vis) == 3 && vis["id"] && vis["name"] && vis["dept"])
	_, err = pol.Visible("ghost")
	check("policy: unknown role yields ErrUnknownRole", errors.Is(err, policy.ErrUnknownRole))
	empty, err := pol.Visible("auditor")
	check("policy: explicit empty grant differs from unknown role", err == nil && len(empty) == 0)

	tree := predicate.AndOf(
		predicate.Equal("id", 7),
		predicate.OrOf(predicate.Equal("name", "ada"), predicate.NotOf(predicate.Equal("dept", "ops"))),
	)
	check("predicate: node count matches tree shape", predicate.Count(tree) == 6)
	foldOr := predicate.Fold(predicate.OrOf(predicate.ConstBool(true), predicate.Equal("secret", 1)))
	foldAnd := predicate.Fold(predicate.AndOf(predicate.ConstBool(true), predicate.Equal("secret", 1)))
	check("predicate: folding drops dead branches, keeps live ones",
		foldOr.Kind == predicate.Const && foldAnd.Kind == predicate.Eq && foldAnd.Col == "secret")

	var refErr *filter.RefError
	err = (&filter.Filter{}).Check(predicate.NotOf(predicate.Equal("secret", 1)), vis)
	named := errors.As(err, &refErr) && len(refErr.Refs) == 1 &&
		refErr.Refs[0].Col == "secret" && refErr.Refs[0].Path == "$/not"
	check("filter: NOT (secret = 1) rejected, names column and path",
		errors.Is(err, filter.ErrInvisibleColumn) && named)
	err = (&filter.Filter{}).Check(predicate.IsNullOf("secret"), vis)
	check("filter: secret IS NULL rejected, no rows returned", errors.Is(err, filter.ErrInvisibleColumn))
	err = (&filter.Filter{}).Check(predicate.OrOf(predicate.ConstBool(true), predicate.Equal("secret", 1)), vis)
	check("filter: TRUE OR (secret=1) allowed, branch folded away", err == nil)

	row := map[string]any{"id": 7, "name": "ada", "dept": "eng", "secret": 42}
	pruned, dropped := (&filter.Filter{}).PruneRow(row, vis)
	_, leaked := pruned["secret"]
	zeroFilled := map[string]any{"id": 7, "name": "ada", "dept": "eng", "secret": nil}
	check("filter: pruned row drops invisible cols, differs from zero-fill",
		!leaked && len(pruned) == 3 && len(dropped) == 1 && dropped[0] == "secret" &&
			!maps.Equal(pruned, zeroFilled))

	visits := &filter.Filter{}
	fullTreeVis := map[string]bool{"id": true, "name": true, "dept": true}
	_ = visits.Check(tree, fullTreeVis)
	check("filter: visited nodes == tree nodes, single pass", visits.Visited() == predicate.Count(tree))

	big := make(map[string]any, 1000)
	for i := 0; i < 1000; i++ {
		big[fmt.Sprintf("c%d", i)] = i
	}
	five := map[string]bool{"c0": true, "c1": true, "c2": true, "c3": true, "c4": true}
	copier := &filter.Filter{}
	out, _ := copier.PruneRow(big, five)
	check("filter: 1000 cols / 5 visible, copies 5 <= 4*5",
		len(out) == 5 && copier.Copies() == 5 && copier.Copies() <= 4*5)

	check("filter: 32 exhaustive combos match DESIGN.md", exhaustive() == 0)

	emptyVis := map[string]bool{}
	fullVis := map[string]bool{"a": true, "b": true, "c": true, "secret": true}
	e1 := (&filter.Filter{}).Check(predicate.Equal("a", 1), emptyVis)
	e2 := (&filter.Filter{}).Check(predicate.Equal("a", 1), fullVis)
	e3 := (&filter.Filter{}).Check(nil, emptyVis)
	e4 := (&filter.Filter{}).Check(predicate.ConstBool(true), emptyVis)
	check("filter: empty/full visibility, nil and const-only predicates",
		errors.Is(e1, filter.ErrInvisibleColumn) && e2 == nil && e3 == nil && e4 == nil)

	base := buildReport(0)
	same := true
	for seed := int64(1); seed < 20; seed++ {
		if buildReport(seed) != base {
			same = false
		}
	}
	check("report: byte-identical across 20 shuffled builds", same)

	status := "OK"
	if failed > 0 {
		status = "FAIL"
	}
	fmt.Printf("%s TOTAL %d/%d checks passed\n", status, passed, passed+failed)
	if failed > 0 {
		os.Exit(1)
	}
}

// buildReport records the same pruning facts in a seed-dependent order and
// returns the canonical rendering; every seed must yield identical bytes.
func buildReport(seed int64) string {
	rep := report.New()
	rep.RejectedQuery = true
	cols := []string{"secret", "salary", "ssn", "token"}
	for _, i := range rand.New(rand.NewSource(seed)).Perm(len(cols)) {
		rep.Drop(cols[i])
	}
	refs := []filter.Ref{
		{Col: "secret", Path: "$/or[0]"},
		{Col: "secret", Path: "$/or[1]/not"},
		{Col: "salary", Path: "$/and[0]"},
	}
	for _, i := range rand.New(rand.NewSource(seed + 100)).Perm(len(refs)) {
		rep.Reject(refs[i].Col, refs[i].Path)
	}
	return rep.Canonical()
}

// exhaustive runs the 32-combo matrix from DESIGN.md section 5 and returns
// the number of combos whose outcome deviates from the documented rule:
// reject iff the folded predicate still references an invisible column.
func exhaustive() int {
	cols := []string{"a", "b", "c"}
	shapes := []struct {
		refs map[string]bool
		make func() *predicate.Node
	}{
		{map[string]bool{"a": true}, func() *predicate.Node { return predicate.Equal("a", 1) }},
		{map[string]bool{"a": true}, func() *predicate.Node { return predicate.NotOf(predicate.Equal("a", 1)) }},
		{map[string]bool{"a": true, "b": true}, func() *predicate.Node {
			return predicate.AndOf(predicate.Equal("a", 1), predicate.Equal("b", 2))
		}},
		{map[string]bool{"a": true, "b": true, "c": true}, func() *predicate.Node {
			return predicate.OrOf(predicate.Equal("a", 1), predicate.Equal("b", 2), predicate.Equal("c", 3))
		}},
	}
	bad := 0
	for mask := 0; mask < 8; mask++ {
		vis := map[string]bool{}
		for i, c := range cols {
			if mask&(1<<i) != 0 {
				vis[c] = true
			}
		}
		for _, s := range shapes {
			err := (&filter.Filter{}).Check(s.make(), vis)
			wantReject := false
			for c := range s.refs {
				if !vis[c] {
					wantReject = true
				}
			}
			if (err != nil) != wantReject {
				bad++
			}
		}
	}
	return bad
}
