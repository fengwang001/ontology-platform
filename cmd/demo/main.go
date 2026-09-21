// Command demo prints OK/FAIL verdicts for the slashpath sample tables.
package main

import (
	"fmt"
	"os"

	"ontology/internal/slashpath"
)

func main() {
	fails := 0
	check := func(got, want string) {
		verdict := "OK"
		if got != want {
			verdict = "FAIL"
			fails++
		}
		fmt.Printf("%s got=%q want=%q\n", verdict, got, want)
	}

	fmt.Println("== Clean ==")
	for _, c := range [][2]string{
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
	} {
		check(slashpath.Clean(c[0]), c[1])
	}

	fmt.Println("== Join ==")
	check(slashpath.Join("a", "b"), "a/b")
	check(slashpath.Join("a", "", "b"), "a/b")
	check(slashpath.Join("/a", "b"), "/a/b")
	check(slashpath.Join("a", "../b"), "b")
	check(slashpath.Join(), ".")
	check(slashpath.Join("", ""), ".")

	fmt.Printf("== %d failure(s) ==\n", fails)
	if fails > 0 {
		os.Exit(1)
	}
}
