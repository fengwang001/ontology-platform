// Command demo exercises the ontology equivalence-class merger and
// prints one OK/FAIL verdict line per check. It takes no arguments,
// uses no network, and exits 0 when every check passes.
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strings"

	"ontology"
)

var failures int

func report(ok bool, format string, args ...any) {
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
		failures++
	}
	fmt.Printf(verdict+" "+format+"\n", args...)
}

func chainID(i int) string { return fmt.Sprintf("v%05d", i) }

func serialize(classes [][]string) string {
	parts := make([]string, len(classes))
	for i, class := range classes {
		parts[i] = "{" + strings.Join(class, ",") + "}"
	}
	return strings.Join(parts, " ")
}

func main() {
	// 1. Representatives after a batch of unions.
	var ds ontology.DisjointSet
	for _, p := range [][2]string{{"bravo", "alpha"}, {"charlie", "bravo"}, {"zinc", "yttrium"}} {
		ds.Union(p[0], p[1])
	}
	classes := ds.Classes()
	reps := make([]string, len(classes))
	for i, class := range classes {
		reps[i] = class[0]
	}
	report(len(reps) == 2 && reps[0] == "alpha" && reps[1] == "yttrium",
		"representatives after unions: %v", reps)

	// 2. Merging a smaller ID later updates the representative.
	ds.Union("aardvark", "charlie")
	rep, _ := ds.Find("bravo")
	report(rep == "aardvark", "late smaller ID takes over: Find(bravo)=%q", rep)

	// 3. Shuffled union order yields byte-identical Classes output.
	pairs := [][2]string{{"alpha", "bravo"}, {"bravo", "charlie"}, {"zinc", "yttrium"}, {"aardvark", "charlie"}}
	base := serialize(ds.Classes())
	same := true
	for seed := int64(1); seed <= 20; seed++ {
		shuffled := append([][2]string(nil), pairs...)
		rand.New(rand.NewSource(seed)).Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		var alt ontology.DisjointSet
		for _, p := range shuffled {
			alt.Union(p[0], p[1])
		}
		if serialize(alt.Classes()) != base {
			same = false
		}
	}
	report(same, "20 shuffled union orders give identical Classes: %s", base)

	// 4. Path compression on a 20001-element chain.
	var chain ontology.DisjointSet
	for i := 0; i < 20_000; i++ {
		chain.Union(chainID(i), chainID(i+1))
	}
	_, firstHops, _ := chain.FindWithHops(chainID(20_000))
	total := 0
	for i := 0; i < 10_000; i++ {
		_, hops, _ := chain.FindWithHops(chainID(20_000))
		total += hops
	}
	avg := float64(total) / 10_000
	report(avg < 3, "chain of 20001: first Find=%d hops, follow-up avg=%.2f hops (<3)", firstHops, avg)

	// 5. Unknown element: Find errors, Union creates.
	var fresh ontology.DisjointSet
	_, findErr := fresh.Find("ghost")
	fresh.Union("ghost", "shadow")
	_, findErrAfter := fresh.Find("ghost")
	report(errors.Is(findErr, ontology.ErrUnknownElement) && findErrAfter == nil,
		"unknown element: Find errors (%v), Union creates implicitly", findErr)

	// 6. Connected: error for unknown is distinct from plain false.
	fresh.Add("solo")
	okAB, errAB := fresh.Connected("ghost", "solo")
	_, errUnknown := fresh.Connected("ghost", "nobody")
	report(!okAB && errAB == nil && errors.Is(errUnknown, ontology.ErrUnknownElement),
		"Connected: false (known pair) vs error (unknown ID) are distinct")

	// 7. Repeating a union never shrinks the class count.
	ds.Union("alpha", "zinc")
	before := ds.ClassCount()
	for i := 0; i < 1_000_000; i++ {
		ds.Union("alpha", "zinc")
	}
	report(ds.ClassCount() == before,
		"1M repeated unions: class count stays %d", before)

	// Summary.
	if failures == 0 {
		fmt.Println("OK   all 7 checks passed")
		return
	}
	fmt.Printf("FAIL %d of 7 checks failed\n", failures)
	os.Exit(1)
}
