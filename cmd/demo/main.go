package main

import (
	"fmt"
	"os"

	"ontology/qp"
)

type check struct {
	name string
	pass bool
}

func main() {
	var checks []check

	checks = append(checks, check{"trailing-space-before-newline", false})
	checks = append(checks, check{"interior-space-unchanged", false})
	checks = append(checks, check{"trailing-space-at-eof", false})
	checks = append(checks, check{"equals-token-not-split", false})
	checks = append(checks, check{"76-column-limit-includes-soft-equals", false})
	checks = append(checks, check{"five-error-kinds-and-offsets", false})
	checks = append(checks, check{"all-split-points-consistent", false})
	checks = append(checks, check{"roundtrip", false})
	checks = append(checks, check{"minimal-escaping", false})
	checks = append(checks, check{"check-count-budget", false})

	_ = qp.Encode

	pass := 0
	for _, c := range checks {
		status := "FAIL"
		if c.pass {
			status = "OK"
			pass++
		}
		fmt.Printf("%s %s\n", status, c.name)
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		os.Exit(1)
	}
}
