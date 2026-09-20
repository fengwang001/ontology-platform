package main

import (
	"errors"
	"fmt"

	"ontology/negotiate"
)

var failed int

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed++
	}
	fmt.Printf("%-4s %s\n", status, name)
}

func main() {
	o, r, err := negotiate.Select("text/plain;q=0.5, text/html;q=0.9", []string{"text/plain", "text/html"})
	check("1 q-value ordering", err == nil && o == "text/html" && r == "text/html;q=0.9")

	o, r, err = negotiate.Select("text/*;q=0.9, text/html;q=0.1", []string{"text/html"})
	check("2 specificity beats q", err == nil && o == "text/html" && r == "text/html;q=0.1")

	o, _, err = negotiate.Select("text/html;q=0, */*;q=1", []string{"text/html", "text/plain"})
	_, _, err2 := negotiate.Select("text/html;q=0, */*;q=1", []string{"text/html"})
	check("3 q=0 explicit reject", err == nil && o == "text/plain" && errors.Is(err2, negotiate.ErrNotAcceptable))

	offers := []string{"text/html", "text/plain"}
	o1, r1, _ := negotiate.Select("text/*", offers)
	o2, r2, _ := negotiate.Select("text/*", offers)
	check("4 server-order tie break", o1 == "text/html" && o1 == o2 && r1 == r2)

	o, r, err = negotiate.Select("text/html;level=1;q=0.5;ignored=x", []string{"text/html;level=1", "text/html"})
	check("5 media type params", err == nil && o == "text/html;level=1" && r == "text/html;level=1;q=0.5;ignored=x")

	o, _, err = negotiate.Select("  ", offers)
	_, _, err2 = negotiate.Select("*/*", nil)
	check("6 empty/default handling", err == nil && o == "text/html" && errors.Is(err2, negotiate.ErrNotAcceptable))

	bad := []string{"texthtml", "/html", "text/", "text/html;level", "text/html;q=1.5"}
	ok := true
	for _, b := range bad {
		so, sr, se := negotiate.Select(b, offers)
		ok = ok && errors.Is(se, negotiate.ErrMalformed) && so == "" && sr == ""
	}
	check("7 malformed -> ErrMalformed", ok)

	snapshot := append([]string(nil), offers...)
	a1, b1, _ := negotiate.Select("text/*;q=0.8, */*;q=0.1", offers)
	a2, b2, _ := negotiate.Select("text/*;q=0.8, */*;q=0.1", offers)
	same := a1 == a2 && b1 == b2
	for i := range offers {
		same = same && offers[i] == snapshot[i]
	}
	check("8 no mutation, repeatable", same)

	fmt.Printf("---\n%d/8 semantics OK\n", 8-failed)
}
