// Command demo runs the expression parser/evaluator self-checks and prints
// one OK/FAIL line per check. It takes no arguments and uses no network.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/lex"
	"ontology/pratt"
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

func evalOK(s string, want int64) bool {
	v, err := api.Eval(s)
	return err == nil && v == want
}

func main() {
	toks, err := lex.Lex("2^3^2")
	ok := err == nil && len(toks) == 5 && toks[0].Kind == lex.NUMBER && toks[0].Val == 2
	_, err = lex.Lex("2&3")
	check("lex: tokens + illegal char", ok && errors.Is(err, lex.ErrIllegalChar))

	// Section 3: subresults of 2^3^2 are 2, then 3^2=9, then 2^9=512.
	check("steps of 2^3^2: 2, 9, 512",
		evalOK("2", 2) && evalOK("3^2", 9) && evalOK("2^3^2", 512))

	check("values: 10-4-3=3 2^3^2=512 -2^2=4 2^10=1024",
		evalOK("10-4-3", 3) && evalOK("2^3^2", 512) &&
			evalOK("-2^2", 4) && evalOK("2^10", 1024))

	check("values: - -3=3 1- -2=3 (1+2)^2=9",
		evalOK("- -3", 3) && evalOK("1- -2", 3) && evalOK("(1+2)^2", 9))

	// Four decidable, mutually distinct error classes.
	classes := []struct {
		expr string
		sent error
	}{
		{"1&2", lex.ErrIllegalChar},
		{"(1+2", pratt.ErrParens},
		{"2^(0-1)", pratt.ErrExponent},
		{"1/0", pratt.ErrDivZero},
	}
	seen := map[error]bool{}
	ok = true
	for _, c := range classes {
		_, err := api.Eval(c.expr)
		ok = ok && errors.Is(err, c.sent) && !seen[c.sent]
		seen[c.sent] = true
	}
	check("errors: 4 distinct classes", ok && len(seen) == 4)

	check("state unchanged after rejection", evalOK("2+3", 5))

	check("comparisons independent of m", pratt.SelfCheck() == nil)

	// Concurrent evaluation must agree value-for-value.
	exprs := []string{"2^3^2", "-2^2", "(1+2)^2", "10-4-3", "2^10", "- -3"}
	want := make([]int64, len(exprs))
	for i, e := range exprs {
		want[i], _ = api.Eval(e)
	}
	const workers = 16
	results := make([][]int64, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			results[w] = make([]int64, len(exprs))
			for i, e := range exprs {
				results[w][i], _ = api.Eval(e)
			}
		}(w)
	}
	wg.Wait()
	ok = true
	for w := 0; w < workers; w++ {
		for i := range want {
			ok = ok && results[w][i] == want[i]
		}
	}
	check("concurrent eval agrees", ok)

	check("api.SelfCheck: four invariants", api.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
