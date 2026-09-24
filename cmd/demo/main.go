// Command demo exercises the property-level permission filter end to end.
package main

import (
	"fmt"
	"os"

	"ontology/policy"
	"ontology/predicate"
)

var failures int

func check(name string, ok bool) {
	status := "OK  "
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	pol := policy.New()
	pol.Grant("analyst", "id")
	pol.Grant("analyst", "name")

	check("policy: grants resolve per role, ungranted column invisible",
		pol.Visible("analyst", "id") && !pol.Visible("analyst", "secret") &&
			!pol.Visible("ghost", "id") && len(pol.Columns("analyst")) == 2)

	folded := predicate.Fold(predicate.Or{
		L: predicate.Const{Value: true},
		R: predicate.Cmp{Column: "secret", Op: predicate.Eq, Value: 1},
	})
	fc, isConst := folded.(predicate.Const)
	tree := predicate.And{
		L: predicate.Cmp{Column: "a", Op: predicate.Eq, Value: 1},
		R: predicate.Or{
			L: predicate.Not{X: predicate.Cmp{Column: "b", Op: predicate.Eq, Value: 2}},
			R: predicate.Cmp{Column: "c", Op: predicate.Gt, Value: 3},
		},
	}
	check("predicate: fold kills dead OR branch, Count matches",
		isConst && fc.Value && predicate.Count(tree) == 6)

	if failures > 0 {
		fmt.Printf("FAIL total: %d check(s) failed\n", failures)
		os.Exit(1)
	}
	fmt.Println("OK  total: all checks passed")
}
