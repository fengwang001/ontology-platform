package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"

	"ontology/filter"
	"ontology/policy"
	"ontology/predicate"
)

var failed int

func check(name string, ok bool, detail string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed++
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}

func main() {
	pol := policy.New()
	pol.Grant("analyst", "name", "age")
	vis, registered := pol.Visible("analyst")
	_, ghost := pol.Visible("ghost")
	check("policy", registered && !ghost && len(vis) == 2 && !pol.IsVisible("analyst", "secret"),
		"role->columns mapping, unregistered role sees nothing")

	foldedTrue := predicate.Fold(predicate.Or{L: predicate.Bool(true), R: predicate.Eq("secret", 1)})
	foldedKept := predicate.Fold(predicate.Or{L: predicate.Bool(false), R: predicate.Eq("secret", 1)})
	c, isConst := foldedTrue.(predicate.Const)
	cmp, isCmp := foldedKept.(predicate.Cmp)
	tree := predicate.Not{X: predicate.And{L: predicate.Eq("a", 1), R: predicate.Eq("b", 2)}}
	check("predicate", isConst && c.Value && isCmp && cmp.Column == "secret" && predicate.Count(tree) == 4,
		"OR short-circuit fold discards unread branch; node count")

	eng := filter.New(pol, []string{"name", "age", "secret"})

	notProbe := predicate.Not{X: predicate.Eq("secret", 1)}
	errNot := eng.Check("analyst", notProbe)
	visitedAfterNot := eng.Visited()
	var invErr *filter.InvisibleError
	named := errors.As(errNot, &invErr) && len(invErr.Refs) == 1 &&
		invErr.Refs[0].Column == "secret" && invErr.Refs[0].Path == "root.not"
	check("probe NOT(secret=1)", errors.Is(errNot, filter.ErrInvisibleColumn) && named,
		"rejected, names column and path")

	errNull := eng.Check("analyst", predicate.IsNull("secret"))
	check("probe secret IS NULL", errors.Is(errNull, filter.ErrInvisibleColumn),
		"rejected instead of answering")

	row := map[string]any{"name": "ada", "age": 36, "secret": 1}
	pruned, _ := eng.PruneRow("analyst", row)
	zeroed := map[string]any{"name": "ada", "age": 36, "secret": 0}
	_, leaked := pruned["secret"]
	check("row prune", !leaked && len(pruned) == 2 && !reflect.DeepEqual(pruned, zeroed),
		"invisible column removed, distinguishable from zeroed")

	check("visited == nodes", visitedAfterNot == predicate.Count(notProbe),
		"single-pass predicate walk")

	schema1k := make([]string, 1000)
	row1k := make(map[string]any, 1000)
	for i := range schema1k {
		schema1k[i] = fmt.Sprintf("c%d", i)
		row1k[schema1k[i]] = i
	}
	pol.Grant("peep", schema1k[:5]...)
	eng1k := filter.New(pol, schema1k)
	_, _ = eng1k.PruneRow("peep", row1k)
	check("copy bound 1000/5", eng1k.Copies() == 5 && eng1k.Copies() <= 4*5,
		"copies <= 4*visible, independent of row width")

	pol.Grant("blind")
	pol.Grant("allseeing", "name", "age", "secret")
	errBlind := eng.Check("blind", predicate.Eq("name", "x"))
	emptyRow, _ := eng.PruneRow("blind", row)
	errAll := eng.Check("allseeing", notProbe)
	check("empty/full visibility", errors.Is(errBlind, filter.ErrInvisibleColumn) &&
		len(emptyRow) == 0 && errAll == nil,
		"empty set rejects all refs; full set passes")

	engEx := filter.New(pol, []string{"a", "b", "c"})
	check("exhaustive 32", exhaustiveOK(pol, engEx), "8 visibility x 4 shapes match definition")

	if failed > 0 {
		os.Exit(1)
	}
	fmt.Println("OK total: all checks passed")
}

// exhaustiveOK checks all 8 visibility masks against the four predicate
// shapes: reject iff referenced columns intersect the invisible set.
func exhaustiveOK(pol *policy.Policy, eng *filter.Engine) bool {
	all := []string{"a", "b", "c"}
	shapes := []struct {
		refs map[string]bool
		pred predicate.Node
	}{
		{map[string]bool{"a": true}, predicate.Eq("a", 1)},
		{map[string]bool{"b": true}, predicate.Not{X: predicate.Eq("b", 1)}},
		{map[string]bool{"a": true, "b": true},
			predicate.And{L: predicate.Eq("a", 1), R: predicate.Eq("b", 1)}},
		{map[string]bool{"b": true, "c": true},
			predicate.Or{L: predicate.Eq("b", 1), R: predicate.Eq("c", 1)}},
	}
	for mask := 0; mask < 8; mask++ {
		var vis []string
		for i, c := range all {
			if mask&(1<<i) != 0 {
				vis = append(vis, c)
			}
		}
		role := fmt.Sprintf("r%d", mask)
		pol.Grant(role, vis...)
		for _, sh := range shapes {
			err := eng.Check(role, sh.pred)
			shouldReject := false
			for i, c := range all {
				if mask&(1<<i) == 0 && sh.refs[c] {
					shouldReject = true
				}
			}
			if shouldReject == (err == nil) {
				return false
			}
		}
	}
	return true
}
