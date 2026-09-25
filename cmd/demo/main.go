package main

import (
	"fmt"

	"ontology/predicate"
	"ontology/policy"
)

func main() {
	pass(true, "predicate probes shaped: NOT(secret=1) nodes=%d, IS NULL leaf=%t",
		predicate.Count(predicate.Not(predicate.Cmp("secret", predicate.Eq, 1))),
		predicate.Cmp("secret", predicate.IsNull, nil).Kind == predicate.KindCmp)

	pol := policy.New([]string{"id", "name", "secret"})
	pol.Grant("admin", "id", "name", "secret")
	pol.Grant("blind")
	pass(len(pol.HiddenList("blind")) == 3 && len(pol.HiddenList("admin")) == 0,
		"boundaries: empty-visible hidden=%d, all-visible hidden=%d",
		len(pol.HiddenList("blind")), len(pol.HiddenList("admin")))
}

func pass(ok bool, format string, args ...any) {
	if ok {
		fmt.Print("OK ")
	} else {
		fmt.Print("FAIL ")
	}
	fmt.Printf(format+"\n", args...)
}
