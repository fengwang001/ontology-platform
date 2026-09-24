package main

import (
	"errors"
	"fmt"

	"ontology/filter"
	"ontology/policy"
	"ontology/predicate"
	"ontology/report"
)

func main() {
	var pass, fail int
	check := func(name string, ok bool) {
		if ok {
			pass++
			fmt.Println("OK", name)
		} else {
			fail++
			fmt.Println("FAIL", name)
		}
	}
	_ = check

	// policy: empty grant removes every key; full grant keeps every key.
	{
		pol := policy.New("id", "region", "secret")
		_ = pol.Grant("all", "id", "secret")
		empty := pol.Project("none", map[string]string{"id": "7", "secret": "s"})
		full := pol.Project("all", map[string]string{"id": "7", "secret": "s"})
		check("empty visible set projects no columns", len(empty) == 0)
		check("full visible set projects all columns", len(full) == 2)
	}

	// predicate: OR with a constant-true arm folds to a constant, which is the
	// structural basis of the short-circuit exception.
	{
		tree := predicate.NewOr(predicate.NewConst(true), predicate.NewEq("secret", "1"))
		folded, isConst, val := predicate.Fold(tree)
		_ = folded
		check("OR true arm constant-folds", isConst && val)
	}

	// filter: approach (3) — probes on invisible columns reject with name+path;
	// removed keys are distinguishable from empty values; costs are bounded.
	{
		pol := policy.New("id", "region", "secret")
		_ = pol.Grant("viewer", "id", "region")
		an := filter.NewAnalyzer(pol)

		notTree := predicate.NewNot(predicate.NewEq("secret", "1"))
		_, notRefs, notErr := an.Analyze("viewer", notTree)
		notOK := errors.Is(notErr, filter.ErrInvisibleColumn) &&
			len(notRefs) == 1 && notRefs[0].Column == "secret" &&
			fmt.Sprint(notRefs[0].Path) == "[0]"
		check("NOT(secret=1) rejected naming secret at path [0]", notOK)

		_, _, nullErr := an.Analyze("viewer", predicate.NewIsNull("secret"))
		check("secret IS NULL rejected", errors.Is(nullErr, filter.ErrInvisibleColumn))

		rows := []filter.Row{{"id": "7", "region": "cn", "secret": ""}}
		projected := an.ProjectRows("viewer", rows)
		_, present := projected[0]["secret"]
		rowOK := projected[0]["id"] == "7" && projected[0]["region"] == "cn" &&
			!present
		check("trimmed row lacks invisible key (distinct from empty)", rowOK)

		countTree := predicate.NewAnd(
			predicate.NewOr(predicate.NewEq("id", "7"), predicate.NewConst(false)),
			predicate.NewNot(predicate.NewEq("region", "cn")))
		_, _, _ = an.Analyze("viewer", countTree)
		check("nodes visited equals node count",
			an.NodesVisited() == predicate.Count(countTree))

		bigCols := makeBigCols()
		big := policy.New(bigCols...)
		_ = big.Grant("r", bigCols[:5]...)
		bigAn := filter.NewAnalyzer(big)
		bigRow := filter.Row{}
		for _, c := range bigCols {
			bigRow[c] = "v"
		}
		bigAn.ProjectRows("r", []filter.Row{bigRow})
		check("1000 cols / 5 visible: copies <= 20", bigAn.Copies() <= 4*5)
	}

	// report: shuffling construction order 20 times must not change one byte.
	{
		pol := policy.New("id", "secret", "other")
		_ = pol.Grant("viewer", "id")
		refs := []filter.Ref{
			{Column: "secret", Path: []int{1}},
			{Column: "other", Path: []int{0}},
			{Column: "secret", Path: []int{0, 1}},
		}
		base := report.Build(pol, "viewer", refs, true, 4, 1).String()
		stable := true
		for iter := 0; iter < 20; iter++ {
			sh := append([]filter.Ref(nil), refs...)
			seed := uint64(iter*7 + 3)
			for i := len(sh) - 1; i > 0; i-- {
				j := int(seed % uint64(i+1))
				seed = seed*1103515245 + 12345
				sh[i], sh[j] = sh[j], sh[i]
			}
			if report.Build(pol, "viewer", sh, true, 4, 1).String() != base {
				stable = false
			}
		}
		check("report byte-identical over 20 shuffles", stable)

		// exhaustive: 3 columns x 8 visibility masks x 4 predicate forms.
		type tally struct{ allowed, rejected int }
		tallies := map[string]*tally{
			"=": {}, "NOT": {}, "AND": {}, "OR": {},
		}
		bad := 0
		for mask := 0; mask < 8; mask++ {
			vis := func(i int) bool { return mask&(1<<i) != 0 }
			forms := []struct {
				name string
				tree func() *predicate.Node
			}{
				{"=", func() *predicate.Node { return predicate.NewEq("a", "1") }},
				{"NOT", func() *predicate.Node {
					return predicate.NewNot(predicate.NewEq("a", "1"))
				}},
				{"AND", func() *predicate.Node {
					return predicate.NewAnd(predicate.NewEq("a", "1"),
						predicate.NewEq("b", "2"), predicate.NewEq("c", "3"))
				}},
				{"OR", func() *predicate.Node {
					return predicate.NewOr(predicate.NewEq("a", "1"),
						predicate.NewEq("b", "2"), predicate.NewEq("c", "3"))
				}},
			}
			for _, f := range forms {
				cols := []string{"a", "b", "c"}
				grant := []string{}
				for i, c := range cols {
					if vis(i) {
						grant = append(grant, c)
					}
				}
				p := policy.New(cols...)
				_ = p.Grant("r", grant...)
				_, _, _, err := filter.Apply(p, "r", f.tree(), nil)
				gotAllowed := err == nil
				wantAllowed := true
				switch f.name {
				case "=", "NOT":
					wantAllowed = vis(0)
				case "AND", "OR":
					wantAllowed = vis(0) && vis(1) && vis(2)
				}
				if gotAllowed != wantAllowed {
					bad++
				}
				if gotAllowed {
					tallies[f.name].allowed++
				} else {
					tallies[f.name].rejected++
				}
			}
		}
		wantTally := map[string][2]int{
			"=": {4, 4}, "NOT": {4, 4}, "AND": {1, 7}, "OR": {1, 7},
		}
		counts := true
		for name, want := range wantTally {
			if tallies[name].allowed != want[0] || tallies[name].rejected != want[1] {
				counts = false
			}
		}
		check("32 exhaustive cases match DESIGN (4/4/1/1 allowed)", bad == 0 && counts)
	}

	fmt.Printf("TOTAL pass=%d fail=%d\n", pass, fail)
	if fail != 0 {
		panic("demo checks failed")
	}
}

func makeBigCols() []string {
	cols := make([]string, 0, 1000)
	for i := 0; i < 1000; i++ {
		cols = append(cols, fmt.Sprintf("c%04d", i))
	}
	return cols
}
