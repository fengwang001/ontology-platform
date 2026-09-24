package main

import (
	"fmt"
	"os"

	"ontology/policy"
	"ontology/predicate"
)

type check struct {
	name   string
	ok     bool
	detail string
}

func (c check) line() string {
	if c.ok {
		return "OK " + c.name
	}
	return "FAIL " + c.name + " " + c.detail
}

func policyChecks() []check {
	p := policy.New(map[string][]string{
		"none": {},
		"all":  {"a", "b"},
	})
	empty := !p.Visible("none", "a") && len(p.VisibleSet("none")) == 0
	full := p.Visible("all", "a") && p.Visible("all", "b") && !p.Visible("all", "c")
	return []check{
		{"empty-visible-set sees nothing", empty, ""},
		{"full-visible-set sees all its cols", full, ""},
	}
}

func predicateChecks() []check {
	notProbe := predicate.Not(predicate.Col("secret", predicate.OpEq))
	nullProbe := predicate.Col("secret", predicate.OpIsNull)
	ok := predicate.Validate(notProbe) == nil && predicate.Validate(nullProbe) == nil
	ok = ok && predicate.Count(notProbe) == 2 && predicate.Count(nullProbe) == 1
	return []check{{"probe predicates are well-formed trees", ok, ""}}
}

func main() {
	cs := append(policyChecks(), predicateChecks()...)
	pass := 0
	for _, c := range cs {
		fmt.Println(c.line())
		if c.ok {
			pass++
		}
	}
	fmt.Printf("TOTAL: %d/%d\n", pass, len(cs))
	if pass != len(cs) {
		os.Exit(1)
	}
}
