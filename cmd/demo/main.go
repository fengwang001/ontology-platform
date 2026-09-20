// Command demo exercises the merge3 package and prints OK/FAIL per check.
package main

import (
	"errors"
	"fmt"
	"os"
	"slices"

	"ontology/internal/merge3"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%-4s %s\n", status, name)
}

func merge(base, ours, theirs []string) merge3.Result {
	res, err := merge3.Merge(base, ours, theirs)
	if err != nil {
		fmt.Println("FAIL Merge error:", err)
		failed = true
	}
	return res
}

func main() {
	base := []string{"a", "b", "c"}

	r := merge(base, base, []string{"a", "B", "c"})
	check("one-side change adopted", !r.HasConflict() && slices.Equal(r.Lines, []string{"a", "B", "c"}))

	r = merge(base, []string{"a", "X", "c"}, []string{"a", "X", "c"})
	check("same edit both sides", !r.HasConflict() && slices.Equal(r.Lines, []string{"a", "X", "c"}))

	r = merge(base, []string{"a", "c"}, []string{"a", "B", "c"})
	check("delete vs modify conflicts", r.HasConflict() && len(r.Conflicts[0].Ours) == 0)

	r = merge(base, []string{"a", "c"}, base)
	check("delete vs unchanged", !r.HasConflict() && slices.Equal(r.Lines, []string{"a", "c"}))

	r = merge(base, []string{"a", "O", "b", "c"}, []string{"a", "b", "c", "T"})
	check("disjoint inserts clean", !r.HasConflict() && slices.Equal(r.Lines, []string{"a", "O", "b", "c", "T"}))

	r = merge(base, []string{"a", "O", "b", "c"}, []string{"a", "T", "b", "c"})
	check("same-anchor insert conflicts", r.HasConflict() && len(r.Conflicts[0].Base) == 0)

	r = merge([]string{"h", "x", "t"}, []string{"h", "s", "O", "t"}, []string{"h", "s", "T", "t"})
	check("conflict block minimal", r.HasConflict() && slices.Equal(r.Conflicts[0].Ours, []string{"O"}))

	x := []string{"n1", "a", "n2"}
	r = merge(base, x, x)
	check("Merge(base,x,x)==x", !r.HasConflict() && slices.Equal(r.Lines, x))

	r = merge(base, base, x)
	check("Merge(base,base,y)==y", !r.HasConflict() && slices.Equal(r.Lines, x))

	r = merge(base, []string{"a", "O", "c"}, []string{"a", "T", "c"})
	out, err := r.Render("ours", "theirs")
	check("render diff3", err == nil && slices.Equal(out, []string{
		"a", "<<<<<<< ours", "O", "||||||| base", "b", "=======", "T", ">>>>>>> theirs", "c",
	}))

	_, err = r.Render("", "theirs")
	check("empty label error", errors.Is(err, merge3.ErrEmptyLabel))

	if failed {
		os.Exit(1)
	}
	fmt.Println("all checks passed")
}
