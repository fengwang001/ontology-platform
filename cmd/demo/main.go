// Command demo exercises the attribute-level permission filter end to end.
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"

	"ontology/filter"
	"ontology/policy"
	"ontology/predicate"
	"ontology/report"
)

var passed, failed int

func check(ok bool, msg string) {
	if ok {
		passed++
		fmt.Println("OK  " + msg)
	} else {
		failed++
		fmt.Println("FAIL " + msg)
	}
}

func main() {
	pol := policy.New("analyst", []string{"id", "name"})
	empty := policy.New("nobody", nil)
	check(pol.Visible("name") && !pol.Visible("secret") &&
		!empty.Visible("id") && len(empty.Columns()) == 0,
		"policy: grants exact set, empty set hides everything")

	tree := predicate.NotNode(predicate.Cmp("secret", predicate.Eq, 1))
	got, err := predicate.Eval(tree, map[string]any{"secret": 2})
	check(err == nil && got && predicate.NodeCount(tree) == 2,
		"predicate: NOT(secret=1) evaluates, node count = 2")

	chk := filter.NewChecker(pol)
	err = chk.Check(tree)
	var rej *filter.Rejection
	rejOK := errors.As(err, &rej) && errors.Is(err, filter.ErrInvisibleColumn) &&
		len(rej.Refs) == 1 && rej.Refs[0].Column == "secret" && rej.Refs[0].Path == "$/NOT"
	check(rejOK, "filter: NOT(secret=1) rejected, names secret at $/NOT")

	isNull := predicate.Cmp("secret", predicate.IsNull, nil)
	check(errors.Is(filter.NewChecker(pol).Check(isNull), filter.ErrInvisibleColumn),
		"filter: secret IS NULL rejected (probe 乙 fails)")

	row := map[string]any{"id": 1, "name": "ada", "secret": 0}
	pruned, copies := filter.PruneRow(row, pol)
	_, leaked := pruned["secret"]
	zeroed := map[string]any{"id": 1, "name": "ada", "secret": 0}
	check(!leaked && copies == 2 && fmt.Sprint(pruned) != fmt.Sprint(zeroed),
		"filter: pruned row drops secret, differs from zero-valued row")

	big := predicate.OrAll(
		predicate.Cmp("a", predicate.Gt, 1),
		predicate.NotNode(predicate.Cmp("b", predicate.Eq, "x")),
	)
	chk = filter.NewChecker(policy.New("r", []string{"a", "b"}))
	_ = chk.Check(big)
	check(chk.Visited() == predicate.NodeCount(big),
		fmt.Sprintf("filter: visited %d nodes == tree size %d", chk.Visited(), predicate.NodeCount(big)))

	base := report.New(
		[]string{"secret", "salary"},
		[]predicate.Ref{{Column: "secret", Path: "$/OR[1]"}, {Column: "salary", Path: "$/OR[0]"}},
		true,
	).String()
	stable := true
	for seed := int64(0); seed < 20; seed++ {
		rng := rand.New(rand.NewSource(seed))
		cols := []string{"secret", "salary"}
		refs := []predicate.Ref{{Column: "secret", Path: "$/OR[1]"}, {Column: "salary", Path: "$/OR[0]"}}
		rng.Shuffle(len(cols), func(i, j int) { cols[i], cols[j] = cols[j], cols[i] })
		rng.Shuffle(len(refs), func(i, j int) { refs[i], refs[j] = refs[j], refs[i] })
		if report.New(cols, refs, true).String() != base {
			stable = false
		}
	}
	check(stable, "report: byte-identical across 20 shuffled constructions")

	wide := map[string]any{}
	for i := 0; i < 1000; i++ {
		wide[fmt.Sprintf("c%03d", i)] = i
	}
	narrow := policy.New("narrow", []string{"c000", "c001", "c002", "c003", "c004"})
	_, copies = filter.PruneRow(wide, narrow)
	check(copies <= 4*5,
		fmt.Sprintf("filter: prune copies %d <= bound %d (1000 cols, 5 visible)", copies, 4*5))

	forms := []struct {
		name string
		need []string
		make func() *predicate.Node
	}{
		{"=", []string{"a"}, func() *predicate.Node { return predicate.Cmp("a", predicate.Eq, 1) }},
		{"NOT", []string{"a"}, func() *predicate.Node {
			return predicate.NotNode(predicate.Cmp("a", predicate.Eq, 1))
		}},
		{"AND", []string{"a", "b", "c"}, func() *predicate.Node {
			return predicate.AndAll(predicate.Cmp("a", predicate.Eq, 1),
				predicate.Cmp("b", predicate.Eq, 2), predicate.Cmp("c", predicate.Eq, 3))
		}},
		{"OR", []string{"a", "b", "c"}, func() *predicate.Node {
			return predicate.OrAll(predicate.Cmp("a", predicate.Eq, 1),
				predicate.Cmp("b", predicate.Eq, 2), predicate.Cmp("c", predicate.Eq, 3))
		}},
	}
	combos, passes := 0, 0
	match := true
	for mask := 0; mask < 8; mask++ {
		var vis []string
		for i, col := range []string{"a", "b", "c"} {
			if mask&(1<<i) != 0 {
				vis = append(vis, col)
			}
		}
		for _, f := range forms {
			combos++
			want := true
			for _, col := range f.need {
				found := false
				for _, v := range vis {
					found = found || v == col
				}
				want = want && found
			}
			allowed := filter.NewChecker(policy.New("r", vis)).Check(f.make()) == nil
			if allowed != want {
				match = false
			}
			if allowed {
				passes++
			}
		}
	}
	check(match && combos == 32,
		fmt.Sprintf("filter: %d/32 exhaustive combos match definition (%d pass, %d reject)",
			combos, passes, combos-passes))

	constOnly := predicate.OrAll(predicate.Boolean(true), predicate.Cmp("secret", predicate.Eq, 1))
	emptyOK := filter.NewChecker(empty).Check(constOnly) == nil &&
		errors.Is(filter.NewChecker(empty).Check(predicate.Cmp("id", predicate.Eq, 1)), filter.ErrInvisibleColumn)
	full := policy.New("root", []string{"id", "name", "secret"})
	fullOK := filter.NewChecker(full).Check(tree) == nil
	prunedEmpty, _ := filter.PruneRow(row, empty)
	check(emptyOK && fullOK && len(prunedEmpty) == 0,
		"filter: empty set folds const-only OR, rejects refs; full set passes all")

	fmt.Printf("TOTAL %d passed, %d failed\n", passed, failed)
	if failed > 0 {
		os.Exit(1)
	}
}
