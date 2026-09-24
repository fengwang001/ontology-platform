package main

import (
	"fmt"

	"ontology/name"
)

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{checkName()}

	failed := 0
	for _, item := range checks {
		status := "OK"
		if !item.ok {
			status = "FAIL"
			failed++
		}
		fmt.Printf("%s %s\n", status, item.name)
	}
	fmt.Printf("TOTAL %d/%d passed\n", len(checks)-failed, len(checks))
	if failed != 0 {
		panic("demo checks failed")
	}
}

func checkName() check {
	ns := name.NewNamespace("", "a/b")
	ns.Lock()
	err := ns.MoveLocked("", "a/b")
	ns.Unlock()
	return check{"name: empty and slash are ordinary, existing target refused", err != nil}
}
