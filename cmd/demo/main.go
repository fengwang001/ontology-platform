package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strings"

	"ontology/mtype"
	"ontology/negotiate"
	"ontology/rank"
)

var total, failed int

func check(name string, toks ...bool) {
	ok := true
	for _, t := range toks {
		ok = ok && t
	}
	status := "OK"
	if !ok {
		status = "FAIL"
		failed++
	}
	total++
	fmt.Printf("%s %s\n", status, name)
}

func sel(accept string, cands ...string) (string, error) {
	return negotiate.Select(&accept, cands)
}

func main() {
	ts, _ := mtype.Parse(`text/plain;x="a;b"`)
	check("quoted semicolon", len(ts) == 1 && ts[0].Params["x"] == "a;b")

	_, errs := mtype.Parse(`textplain, text/, text/plain;format, text/plain;q=1.5, text/plain;x="abc`)
	wants := []error{mtype.ErrMissingSlash, mtype.ErrEmptySubtype, mtype.ErrParamMissingEq, mtype.ErrBadQ, mtype.ErrUnclosedQuote}
	ok := len(errs) == len(wants)
	for i, w := range wants {
		if ok {
			ok = errors.Is(errs[i], w) && strings.Contains(errs[i].Error(), fmt.Sprintf("item %d", i))
		}
	}
	check("five distinguishable errors", ok)

	cand, _ := mtype.ParseOne("text/plain;format=flowed")
	plain, _ := mtype.ParseOne("text/plain")
	flowed, _ := mtype.ParseOne("text/plain;FORMAT=flowed")
	upper, _ := mtype.ParseOne("text/plain;format=Flowed")
	_, m1 := rank.Score(cand, flowed)
	_, m2 := rank.Score(cand, plain)
	_, m3 := rank.Score(cand, upper)
	check("param set equality", m1 && !m2)
	check("param value case sensitive", !m3)

	g1, _ := sel("text/*, */*", "application/json", "text/html")
	g2, _ := sel("*/*, text/*", "application/json", "text/html")
	g3, _ := sel("text/plain, text/*", "text/html", "text/plain")
	check("specificity order", g1 == "text/html" && g2 == "text/html" && g3 == "text/plain")

	g, _ := sel("text/*;q=0.9, text/plain;q=0.1", "text/plain", "text/html")
	check("q beats specificity", g == "text/html")

	_, err := sel("text/plain;q=0", "text/plain")
	check("q=0 rejects sole candidate", errors.Is(err, negotiate.ErrNotAcceptable))

	_, e1 := sel("text/plain;q=1.5", "text/plain")
	_, e2 := sel("text/plain;q=abc", "text/plain")
	_, e3 := sel("text/plain;q=0.1234", "text/plain")
	g, e4 := sel("text/plain;q=abc, text/html", "text/html")
	badq := negotiate.ErrNotAcceptable
	check("invalid q dropped", errors.Is(e1, badq) && errors.Is(e2, badq) &&
		errors.Is(e3, badq) && e4 == nil && g == "text/html")

	g, _ = sel("text/a, text/b", "text/b", "text/a")
	h, _ := sel("text/b, text/a", "text/b", "text/a")
	check("tie uses server order", g == "text/b" && h == "text/b")

	g, _ = sel("text/a, text/b", "text/a", "text/b")
	h, _ = sel("text/b, text/a", "text/a", "text/b")
	check("accept order irrelevant", g == "text/a" && h == "text/a")

	g, _ = negotiate.Select(nil, []string{"text/plain"})
	_, err = sel("", "text/plain")
	check("missing vs empty accept", g == "text/plain" && errors.Is(err, negotiate.ErrNotAcceptable))

	g, err = sel("image/png", "text/plain")
	check("not acceptable, no fallback", g == "" && errors.Is(err, negotiate.ErrNotAcceptable))

	items := []string{"text/plain;q=0.9", "text/*;q=0.8", "*/*;q=0.5", "application/json;q=0.7"}
	stable := true
	for i := 0; i < 50; i++ {
		perm := rand.New(rand.NewSource(int64(i))).Perm(len(items))
		parts := make([]string, len(items))
		for j, p := range perm {
			parts[j] = items[p]
		}
		g, _ = sel(strings.Join(parts, ", "), "application/json", "text/html", "text/plain")
		stable = stable && g == "text/plain"
	}
	check("50 shuffles stable", stable)

	fmt.Printf("total %d/%d\n", total-failed, total)
	if failed > 0 {
		os.Exit(1)
	}
}
