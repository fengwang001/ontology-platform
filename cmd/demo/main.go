// Command demo prints one OK/FAIL line per BWT property.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"

	"ontology/api"
	"ontology/lf"
	"ontology/rot"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	// Seven-row sorted rotation table of "banana$" plus last/primary.
	t := []byte("banana$")
	rows := make([][]byte, len(t))
	for i := range t {
		rows[i] = append(append([]byte{}, t[i:]...), t[:i]...)
	}
	sort.Slice(rows, func(a, b int) bool { return bytes.Compare(rows[a], rows[b]) < 0 })
	wantRows := []string{"$banana", "a$banan", "ana$ban", "anana$b", "banana$", "na$bana", "nana$ba"}
	ok := len(rows) == len(wantRows)
	for i := range wantRows {
		ok = ok && string(rows[i]) == wantRows[i]
	}
	last, primary := rot.LastColumn(t)
	check("banana table last=annb$aa primary=4", ok && string(last) == "annb$aa" && primary == 4)

	// Terminator already present in input.
	_, _, err := api.Transform([]byte("ban$ana"), '$')
	check("terminator-in-input error", errors.Is(err, api.ErrTerminatorInInput))

	// Wrong reconstruction when starting from row 0 instead of primary:
	// the LF walk rebuilds the rotation "$banana", not "banana$".
	tab, _ := lf.NewTable(last, '$')
	w := make([]byte, len(last))
	row := 0
	for i := len(last) - 1; i >= 0; i-- {
		w[i] = last[row]
		row = tab.LF(row)
	}
	check("row-0 start yields rotation $banana", string(w) == "$banana")

	// Empty and single-character inputs.
	l0, p0, _ := api.Transform(nil, '$')
	l1, p1, _ := api.Transform([]byte("a"), '$')
	r0, _ := api.Inverse(l0, p0, '$')
	r1, _ := api.Inverse(l1, p1, '$')
	check("empty/single roundtrip", string(l0) == "$" && p0 == 0 && string(l1) == "a$" && p1 == 1 &&
		len(r0) == 0 && string(r1) == "a")

	// Roundtrip on a regular string.
	rt, _ := api.Inverse(last, primary, '$')
	check("roundtrip banana", string(rt) == "banana")

	// last is a permutation of the first column F of the sorted matrix.
	f := make([]byte, len(rows))
	for i := range rows {
		f[i] = rows[i][0]
	}
	check("last is permutation of F", bytes.Equal(rot.Sorted(last), f))

	// Three decidable, mutually distinct errors.
	_, _, e1 := api.Transform([]byte("ban$ana"), '$')
	_, e2 := api.Inverse(last, 7, '$')
	_, e3 := api.Inverse([]byte("annb$aa$"), 0, '$')
	check("three distinct sentinel errors", errors.Is(e1, api.ErrTerminatorInInput) &&
		errors.Is(e2, api.ErrInvalidPrimary) && errors.Is(e3, api.ErrTerminatorCount) &&
		!errors.Is(e1, e2) && !errors.Is(e2, e3) && !errors.Is(e1, e3))

	// Rejection leaves no partial output; calls keep working afterwards.
	bad, pbad, _ := api.Transform([]byte("a$a"), '$')
	binv, _ := api.Inverse(last, -1, '$')
	again, _ := api.Inverse(last, primary, '$')
	check("no partial output after rejection", bad == nil && pbad == 0 && binv == nil && string(again) == "banana")

	// Single-LF rank cost stays a small constant as m grows.
	check("rank check count O(1) for m=100..10000", lf.MaxRankChecks(100, 500, 1000, 5000, 10000) <= 4)

	// Concurrent Transform+Inverse matches the serial result byte for byte.
	input := []byte("concurrent bwt roundtrip check")
	serial, sp, _ := api.Transform(input, '$')
	sinv, _ := api.Inverse(serial, sp, '$')
	var wg sync.WaitGroup
	results := make([][]byte, 16)
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			l, p, err := api.Transform(input, '$')
			if err != nil {
				return
			}
			r, err := api.Inverse(l, p, '$')
			if err != nil {
				return
			}
			results[g] = r
		}(g)
	}
	wg.Wait()
	ok = bytes.Equal(sinv, input)
	for _, r := range results {
		ok = ok && bytes.Equal(r, input)
	}
	check("concurrent results identical", ok)

	if failed {
		os.Exit(1)
	}
}
