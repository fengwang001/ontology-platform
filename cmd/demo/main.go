package main

import (
	"fmt"
	"os"

	"ontology/policy"
)

type check struct {
	name string
	ok   bool
	detail string
}

func policyCheck() check {
	p, err := policy.New(map[string][]string{
		"none": {},
		"all":  {"a", "b"},
	})
	if err != nil {
		return check{"policy boundaries", false, err.Error()}
	}
	emptyDenies, _ := p.Visible("none", "a")
	fullAllows, _ := p.Visible("all", "a")
	set, _ := p.VisibleSet("none")
	ok := !emptyDenies && fullAllows && len(set) == 0
	return check{"policy boundaries: empty set denies, full set allows", ok, ""}
}

func main() {
	checks := []check{policyCheck()}
	fails := 0
	for _, c := range checks {
		tag := "OK"
		if !c.ok {
			tag = "FAIL"
			fails++
		}
		fmt.Printf("%s %s %s\n", tag, c.name, c.detail)
	}
	fmt.Printf("TOTAL %d/%d passed\n", len(checks)-fails, len(checks))
	if fails > 0 {
		os.Exit(1)
	}
}
