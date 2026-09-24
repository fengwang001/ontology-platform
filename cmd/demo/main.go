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

func check(ok bool, label string) {
	if ok {
		passed++
		fmt.Println("OK " + label)
	} else {
		failed++
		fmt.Println("FAIL " + label)
	}
}

func main() {
	pol := policy.New()
	pol.Grant("analyst", "dept", "name")
	pol.Grant("empty")
	pol.Grant("admin", "dept", "name", "secret")

	check(pol.Visible("analyst", "name") && !pol.Visible("analyst", "secret") &&
		pol.Known("empty") && len(pol.VisibleSet("empty")) == 0,
		"policy: analyst sees name not secret; empty set known")

	notQ := predicate.Not{Inner: predicate.Compare{Column: "secret", Value: "1"}}
	folded := predicate.Fold(predicate.Or{L: predicate.Const{Value: true}, R: notQ})
	_, foldedToTrue := folded.(predicate.Const)
	check(predicate.NodeCount(notQ) == 2 && foldedToTrue,
		"predicate: NOT(secret=1) has 2 nodes; TRUE OR x folds to TRUE")

	var ck filter.Checker
	var rej *filter.RejectError
	err := ck.Check(notQ, pol, "analyst")
	check(errors.As(err, &rej) && errors.Is(err, filter.ErrInvisibleColumn) &&
		len(rej.Refs) == 1 && rej.Refs[0].Column == "secret" &&
		len(rej.Refs[0].Paths) == 1 && rej.Refs[0].Paths[0] == "$.N",
		"filter: NOT(secret=1) rejected, names secret@$.N")

	err = ck.Check(predicate.IsNull{Column: "secret"}, pol, "analyst")
	check(errors.Is(err, filter.ErrInvisibleColumn),
		"filter: secret IS NULL rejected, no rows returned")

	row := map[string]string{"dept": "eng", "name": "ada", "secret": "x"}
	var pr filter.Pruner
	pruned, err := pr.PruneRow(row, pol, "analyst")
	_, keptSecret := pruned["secret"]
	zeroed := map[string]string{"dept": "eng", "name": "ada", "secret": ""}
	_, zeroHasKey := zeroed["secret"]
	check(err == nil && len(pruned) == 2 && !keptSecret && zeroHasKey != keptSecret,
		"filter: pruned row drops secret, differs from zero-valued")

	big := predicate.And{
		L: predicate.Or{L: predicate.Compare{Column: "name", Value: "a"}, R: predicate.Compare{Column: "dept", Value: "e"}},
		R: predicate.Not{Inner: predicate.Compare{Column: "name", Value: "b"}},
	}
	var ck2 filter.Checker
	check(ck2.Check(big, pol, "analyst") == nil && ck2.Visited() == predicate.NodeCount(big),
		fmt.Sprintf("filter: visited %d nodes == tree size %d", ck2.Visited(), predicate.NodeCount(big)))

	wide := policy.New()
	wide.Grant("r", "c0", "c1", "c2", "c3", "c4")
	bigRow := map[string]string{}
	for i := 0; i < 1000; i++ {
		bigRow[fmt.Sprintf("c%d", i)] = "v"
	}
	var pr2 filter.Pruner
	if _, err := pr2.PruneRow(bigRow, wide, "r"); err != nil {
		failed++
	}
	check(pr2.Copies() <= 4*5,
		fmt.Sprintf("filter: 1000 cols 5 visible, %d copies <= 20", pr2.Copies()))

	check(exhaustive(), "filter: 32/32 exhaustive combos match definition")

	emptyOK := errors.Is(ck.Check(predicate.Compare{Column: "a", Value: "1"}, pol, "empty"), filter.ErrInvisibleColumn) &&
		ck.Check(nil, pol, "empty") == nil
	fullRow, fullErr := pr.PruneRow(row, pol, "admin")
	check(emptyOK && fullErr == nil && len(fullRow) == 3 &&
		ck.Check(predicate.Compare{Column: "secret", Value: "1"}, pol, "admin") == nil,
		"filter: empty set rejects col ref, full set allows all")

	check(reportDeterministic(), "report: 20 shuffled builds byte-identical")

	status := "OK"
	if failed > 0 {
		status = "FAIL"
		defer os.Exit(1)
	}
	fmt.Printf("%s total: %d/%d checks passed\n", status, passed, passed+failed)
}

func cmp(col string) predicate.Compare { return predicate.Compare{Column: col, Value: "1"} }

// reportDeterministic builds the same logical report 20 times with shuffled
// construction order and requires byte-identical output.
func reportDeterministic() bool {
	base := report.New(true, []string{"secret", "ssn"},
		[]filter.Ref{{Column: "secret", Paths: []string{"$.L", "$.R"}}}).String()
	for i := 0; i < 20; i++ {
		rng := rand.New(rand.NewSource(int64(i)))
		pruned := []string{"secret", "ssn"}
		rng.Shuffle(len(pruned), func(a, b int) { pruned[a], pruned[b] = pruned[b], pruned[a] })
		refs := []filter.Ref{
			{Column: "secret", Paths: []string{"$.L"}},
			{Column: "secret", Paths: []string{"$.R"}},
		}
		rng.Shuffle(len(refs), func(a, b int) { refs[a], refs[b] = refs[b], refs[a] })
		if report.New(true, pruned, refs).String() != base {
			return false
		}
	}
	return true
}

// exhaustive verifies all 8 visibility masks x 4 predicate shapes against
// the documented rule: allow iff every referenced column is visible.
func exhaustive() bool {
	cols := []string{"a", "b", "c"}
	abc := map[string]bool{"a": true, "b": true, "c": true}
	forms := []struct {
		build func() predicate.Pred
		refs  map[string]bool
	}{
		{func() predicate.Pred { return cmp("a") }, map[string]bool{"a": true}},
		{func() predicate.Pred { return predicate.Not{Inner: cmp("a")} }, map[string]bool{"a": true}},
		{func() predicate.Pred { return predicate.And{L: predicate.And{L: cmp("a"), R: cmp("b")}, R: cmp("c")} }, abc},
		{func() predicate.Pred { return predicate.Or{L: predicate.Or{L: cmp("a"), R: cmp("b")}, R: cmp("c")} }, abc},
	}
	for mask := 0; mask < 8; mask++ {
		pol := policy.New()
		pol.Grant("r")
		visible := map[string]bool{}
		for i, c := range cols {
			if mask&(1<<i) != 0 {
				pol.Grant("r", c)
				visible[c] = true
			}
		}
		for _, f := range forms {
			wantAllow := true
			for col := range f.refs {
				wantAllow = wantAllow && visible[col]
			}
			err := new(filter.Checker).Check(f.build(), pol, "r")
			if wantAllow != (err == nil) {
				return false
			}
			var rej *filter.RejectError
			if !wantAllow && !(errors.As(err, &rej) && len(rej.Refs) > 0) {
				return false
			}
		}
	}
	return true
}
