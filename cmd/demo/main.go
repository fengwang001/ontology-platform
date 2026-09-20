// Command demo exercises the merge3 package and prints OK/FAIL per rule.
package main

import (
	"errors"
	"fmt"
	"os"
	"slices"

	"ontology/internal/merge3"
)

var failures int

func check(name string, ok bool) {
	status := "OK  "
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", status, name)
}

func merge(base, ours, theirs []string) merge3.Result {
	r, err := merge3.Merge(base, ours, theirs)
	if err != nil {
		panic(err)
	}
	return r
}

func clean(r merge3.Result, want []string) bool {
	return !r.HasConflict() && slices.Equal(r.Lines, want)
}

func main() {
	base := []string{"a", "b", "c", "d", "e", "f", "g"}

	r := merge(base, base, []string{"a", "b", "C", "d", "e", "f", "g"})
	check("one-sided change adopted", clean(r, []string{"a", "b", "C", "d", "e", "f", "g"}))

	r = merge(base, []string{"a", "X", "c", "d", "e", "f", "g"}, []string{"a", "X", "c", "d", "e", "f", "g"})
	check("identical edits merge clean", clean(r, []string{"a", "X", "c", "d", "e", "f", "g"}))

	r = merge(base, []string{"a", "X", "c", "d", "e", "f", "g"}, []string{"a", "Y", "c", "d", "e", "f", "g"})
	check("divergent edits conflict", r.HasConflict() && len(r.Conflicts) == 1)

	r = merge(base, []string{"a", "c", "d", "e", "f", "g"}, []string{"a", "B", "c", "d", "e", "f", "g"})
	check("delete vs modify conflicts", r.HasConflict())

	r = merge(base, []string{"a", "c", "d", "e", "f", "g"}, base)
	check("delete vs untouched deletes", clean(r, []string{"a", "c", "d", "e", "f", "g"}))

	r = merge(base, []string{"a", "x", "b", "c", "d", "e", "f", "g"}, []string{"a", "b", "c", "d", "e", "f", "y", "g"})
	check("disjoint inserts both kept", clean(r, []string{"a", "x", "b", "c", "d", "e", "f", "y", "g"}))

	r = merge(base, []string{"a", "x", "b", "c", "d", "e", "f", "g"}, []string{"a", "y", "b", "c", "d", "e", "f", "g"})
	check("same-anchor inserts conflict", r.HasConflict())

	r = merge(base, []string{"a", "b", "C", "d", "e", "f", "g"}, []string{"a", "b", "c", "d", "e", "f", "G"})
	check("adjacent edits stay separate", clean(r, []string{"a", "b", "C", "d", "e", "f", "G"}))

	x := []string{"n1", "n2", "n3"}
	check("Merge(base,x,x)==x", clean(merge(base, x, x), x))
	check("Merge(base,base,y)==y", clean(merge(base, base, x), x))

	c := merge(base, []string{"a", "X", "c", "d", "e", "f", "g"}, []string{"a", "Y", "c", "d", "e", "f", "g"})
	out, err := c.Render("ours", "theirs")
	check("render diff3 markers", err == nil && slices.Contains(out, "<<<<<<< ours") &&
		slices.Contains(out, "||||||| base") && slices.Contains(out, ">>>>>>> theirs"))
	_, err = c.Render("", "theirs")
	check("empty label rejected", errors.Is(err, merge3.ErrEmptyLabel))

	if failures > 0 {
		fmt.Printf("FAIL: %d check(s) failed\n", failures)
		os.Exit(1)
	}
	fmt.Println("PASS: all checks passed")
}
