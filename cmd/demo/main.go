// Demo runs every acceptance check of the URL canonicalizer and prints
// one OK/FAIL line per check plus a final summary. Exit code is 0 only
// when every check passes.
package main

import (
	"fmt"
	"os"

	"ontology/canon"
)

var failures int

func check(name string, ok bool, detail ...string) {
	status := "OK  "
	if !ok {
		status = "FAIL"
		failures++
	}
	line := fmt.Sprintf("%s %s", status, name)
	if len(detail) > 0 && detail[0] != "" {
		line += " " + detail[0]
	}
	fmt.Println(line)
}

func main() {
	n := canon.New(canon.Config{Mode: canon.ModeSorted})
	ord := canon.New(canon.Config{Mode: canon.ModeOrdered})

	check("idempotency-5000-random", idempotent(n))

	eq1, _ := n.Equivalent("http://h/a%2Fb", "http://h/a/b")
	c1, _ := n.Normalize("http://h/%41%2f")
	check("reserved-escape-kept", !eq1 && c1 == "http://h/A%2F", c1)

	c2, _ := n.Normalize("http://h/../../x")
	eq2, _ := n.Equivalent("http://h/a", "http://h/a/")
	check("dot-resolution-trailing-slash", c2 == "http://h/x" && !eq2, c2)

	qOK := queryChecks(n, ord)
	check("query-three-states-two-modes", qOK)

	check("invalid-escape-kinds", escapeKinds(n))

	eq3, _ := n.Equivalent("http://[2001:0db8:0:0:0:0:0:1]:080/",
		"http://[2001:db8::1]/")
	eq4, _ := n.Equivalent("http://[::1]:8080/", "http://[::1]:8080/")
	check("ipv6-port-normalization", eq3 && eq4)

	check("equivalence-classes", equivClasses(n))

	s1, s2, l1, l2 := scanPair()
	ratio := float64(s2) / float64(s1)
	check("scan-count-linear", ratio > 30 && ratio < 130,
		fmt.Sprintf("L=%d scans=%d L=%d scans=%d ratio=%.1f", l1, s1, l2, s2, ratio))

	check("resource-limits", limits())

	check("inspect-stable", inspectStable(n))

	check("concurrency-matches-serial", concurrent(n))

	total := 11
	fmt.Printf("TOTAL %d/%d passed\n", total-failures, total)
	if failures > 0 {
		os.Exit(1)
	}
}
