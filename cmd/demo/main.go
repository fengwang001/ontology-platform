// Command demo prints OK/FAIL verdicts for the slashpath sample
// tables and the three invariants. It exits non-zero on any FAIL.
package main

import (
	"fmt"
	"os"
	"strings"

	"ontology/internal/slashpath"
)

var failed bool

func check(name, got, want string) {
	verdict := "OK"
	if got != want {
		verdict = "FAIL"
		failed = true
	}
	fmt.Println(fmt.Sprintf("%s %-5s -> %q (want %q)", verdict, name, got, want))
}

func main() {
	cleanCases := []struct{ in, want string }{
		{"/a/b/../c", "/a/c"},
		{"/a/./b", "/a/b"},
		{"a/b/../../c", "c"},
		{"/..", "/"},
		{"//a///b", "/a/b"},
		{"/a/b/", "/a/b"},
		{".", "."},
		{"", "."},
		{"/", "/"},
		{"a/./b/", "a/b"},
	}
	for _, c := range cleanCases {
		check("Clean", slashpath.Clean(c.in), c.want)
	}

	joinCases := []struct {
		elems []string
		want  string
	}{
		{[]string{"a", "b"}, "a/b"},
		{[]string{"a", "", "b"}, "a/b"},
		{[]string{"/a", "b"}, "/a/b"},
		{[]string{"a", "../b"}, "b"},
		{nil, "."},
		{[]string{"", ""}, "."},
	}
	for _, c := range joinCases {
		check("Join", slashpath.Join(c.elems...), c.want)
	}

	// Spot-check the three invariants on a tricky input.
	p := "/../a//./b/../../c"
	once := slashpath.Clean(p)
	inv := slashpath.Clean(once) == once &&
		slashpath.IsAbs(p) == slashpath.IsAbs(once) &&
		!strings.Contains(once, "..")
	verdict := "OK"
	if !inv {
		verdict = "FAIL"
		failed = true
	}
	fmt.Println(fmt.Sprintf("%s invariants on %q -> %q", verdict, p, once))

	if failed {
		os.Exit(1)
	}
}
