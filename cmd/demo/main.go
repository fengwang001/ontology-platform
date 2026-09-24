package main

import (
	"errors"
	"fmt"
)

func report(name string, ok bool, detail string) bool {
	status := "OK"
	if !ok {
		status = "FAIL"
	}
	if detail != "" {
		fmt.Printf("%s  %s  %s\n", status, name, detail)
	} else {
		fmt.Printf("%s  %s\n", status, name)
	}
	return ok
}

func main() {
	checks := []struct {
		name string
		ok   bool
		note string
	}{
		{"format-samples", false, "skeleton"},
		{"roundtrip", false, ""},
		{"reject-one", false, ""},
		{"reject-zero-leadingzero", false, ""},
		{"reject-adjacent", false, ""},
		{"reject-badescape", false, ""},
		{"reject-trailing-backslash", false, ""},
		{"reject-missing-symbol", false, ""},
		{"reject-invalid-utf8", false, ""},
		{"big-count", false, ""},
		{"combining-not-merged", false, ""},
		{"all-splits", false, ""},
		{"byte-counter", false, ""},
	}
	pass := 0
	for _, c := range checks {
		if report(c.name, c.ok, c.note) {
			pass++
		}
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		errors.New("not all checks pass")
	}
}
