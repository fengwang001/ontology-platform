// Command demo exercises the merge3 package against the required
// merge semantics and prints one OK/FAIL verdict per check.
package main

import (
	"fmt"
	"os"
	"reflect"

	"ontology/internal/merge3"
)

var failed bool

func check(name string, got, want any) {
	ok := reflect.DeepEqual(got, want)
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", verdict, name)
	if !ok {
		fmt.Printf("     got=%v want=%v\n", got, want)
	}
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
	base := []string{"a", "b", "c"}

	r := merge(base, base, []string{"a", "B", "c"})
	check("only theirs changed", r.Lines, []string{"a", "B", "c"})

	r = merge(base, []string{"a", "X", "c"}, []string{"a", "X", "c"})
	check("same change both sides", r.Lines, []string{"a", "X", "c"})

	r = merge(base, []string{"a", "X", "c"}, []string{"a", "Y", "c"})
	check("divergent edit conflicts", r.HasConflicts(), true)

	r = merge(base, []string{"a", "c"}, []string{"a", "B", "c"})
	check("delete vs modify conflicts", r.HasConflicts(), true)

	r = merge(base, []string{"a", "c"}, base)
	check("delete vs unchanged", r.Lines, []string{"a", "c"})

	r = merge([]string{"a", "b"}, []string{"a", "O", "b"}, []string{"a", "b", "T"})
	check("disjoint insertions", r.Lines, []string{"a", "O", "b", "T"})

	r = merge([]string{"a", "b"}, []string{"a", "X", "b"}, []string{"a", "Y", "b"})
	check("same-point insert conflicts", r.HasConflicts(), true)

	r = merge(base, base, base)
	check("identity merge", r.HasConflicts(), false)

	r = merge([]string{"h", "m", "t"}, []string{"S", "O", "S"}, []string{"S", "T", "S"})
	check("minimal conflict lines", r.Lines, []string{"S", "S"})

	out, err := r.Render("ours", "theirs")
	check("render err", err, nil)
	check("render head", out[1], "<<<<<<< ours")
	check("render base mark", out[3], "||||||| base")

	_, err = r.Render("", "theirs")
	check("empty label rejected", err, merge3.ErrEmptyLabel)

	if failed {
		os.Exit(1)
	}
	fmt.Println("all demo checks passed")
}
