// Command demo verifies each documented semantic of package negotiate
// and prints one OK/FAIL line per semantic.
package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/negotiate"
)

var failures int

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%-28s %s\n", name, status)
}

func main() {
	offers := []string{"text/html;level=1", "text/html", "text/plain", "application/json"}

	o, _, e := negotiate.Select("text/plain;q=0.5, text/html;q=0.333, application/json;q=0.8", offers)
	check("1 q-value ordering", e == nil && o == "application/json")

	_, r, e := negotiate.Select("text/html;q=0.1, text/*;q=0.9, */*;q=1", []string{"text/html"})
	check("2 specificity beats q", e == nil && r == "text/html;q=0.1")

	o, _, e = negotiate.Select("text/html;q=0, */*;q=1", []string{"text/html", "text/plain"})
	check("3 q=0 explicit reject", e == nil && o == "text/plain")

	tie := []string{"text/plain", "text/html"}
	o1, _, e1 := negotiate.Select("text/*;q=0.7", tie)
	o2, _, e2 := negotiate.Select("text/*;q=0.7", tie)
	check("4 ties keep server order", e1 == nil && e2 == nil && o1 == "text/plain" && o2 == "text/plain")

	o, _, e = negotiate.Select("text/html;level=1;q=0.5;ext=ignored", offers)
	check("5 parameters match", e == nil && o == "text/html;level=1")

	o, r, e = negotiate.Select("  ", offers)
	_, _, eEmpty := negotiate.Select("text/html", nil)
	check("6 empty accept & offers", e == nil && o == offers[0] && r == "*/*" &&
		errors.Is(eEmpty, negotiate.ErrNotAcceptable))

	o, r, e = negotiate.Select("texthtml", offers)
	_, _, eBadQ := negotiate.Select("text/html;q=1.5", offers)
	check("7 malformed -> ErrMalformed", o == "" && r == "" &&
		errors.Is(e, negotiate.ErrMalformed) && errors.Is(eBadQ, negotiate.ErrMalformed))

	snapshot := append([]string(nil), offers...)
	oa, ra, _ := negotiate.Select("text/*;q=0.6, application/json;q=0.9", offers)
	ob, rb, _ := negotiate.Select("text/*;q=0.6, application/json;q=0.9", offers)
	mutated := false
	for i := range offers {
		mutated = mutated || offers[i] != snapshot[i]
	}
	check("8 deterministic, no mutation", !mutated && oa == ob && ra == rb && oa == "application/json")

	if failures > 0 {
		fmt.Printf("%d check(s) failed\n", failures)
		os.Exit(1)
	}
	fmt.Println("all semantics satisfied")
}
