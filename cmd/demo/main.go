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

func check(name string, cond bool) {
	status := "OK"
	if !cond {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%-4s %s\n", status, name)
}

func merge(base, ours, theirs []string) merge3.Result {
	r, err := merge3.Merge(base, ours, theirs)
	if err != nil {
		fmt.Println("FAIL Merge error:", err)
		failed = true
	}
	return r
}

func main() {
	base := []string{"a", "b", "c", "d", "e"}

	r := merge(base, base, []string{"a", "T", "c", "d", "e"})
	check("only theirs changed", !r.HasConflict() &&
		slices.Equal(r.Lines, []string{"a", "T", "c", "d", "e"}))

	both := []string{"a", "X", "c", "d", "e"}
	r = merge(base, both, both)
	check("both sides same change", !r.HasConflict() && slices.Equal(r.Lines, both))

	r = merge(base, []string{"a", "O", "c", "d", "e"}, []string{"a", "T", "c", "d", "e"})
	check("different changes conflict", len(r.Conflicts) == 1)

	r = merge(base, []string{"a", "c", "d", "e"}, []string{"a", "B", "c", "d", "e"})
	check("delete vs modify conflicts", len(r.Conflicts) == 1)

	r = merge(base, []string{"a", "c", "d", "e"}, base)
	check("delete vs untouched", !r.HasConflict() &&
		slices.Equal(r.Lines, []string{"a", "c", "d", "e"}))

	r = merge(base,
		[]string{"a", "O1", "b", "c", "d", "e"},
		[]string{"a", "b", "c", "d", "T1", "e"})
	check("separate insertions kept", !r.HasConflict() && slices.Equal(r.Lines,
		[]string{"a", "O1", "b", "c", "d", "T1", "e"}))

	r = merge(base, []string{"a", "O1", "b", "c", "d", "e"},
		[]string{"a", "T1", "b", "c", "d", "e"})
	check("same-position insert conflicts", len(r.Conflicts) == 1)

	x := []string{"z", "a", "Y", "c"}
	r = merge(base, x, x)
	check("Merge(base,x,x)==x", !r.HasConflict() && slices.Equal(r.Lines, x))

	y := []string{"a", "b", "N"}
	r = merge(base, base, y)
	check("Merge(base,base,y)==y", !r.HasConflict() && slices.Equal(r.Lines, y))

	r = merge(base, []string{"a", "O", "c", "d", "e"}, []string{"a", "T", "c", "d", "e"})
	out, err := r.Render("ours", "theirs")
	check("render diff3", err == nil && slices.Contains(out, "<<<<<<< ours") &&
		slices.Contains(out, "||||||| base") && slices.Contains(out, ">>>>>>> theirs"))

	_, err = r.Render("", "theirs")
	check("empty label rejected", errors.Is(err, merge3.ErrEmptyLabel))

	if failed {
		os.Exit(1)
	}
}
