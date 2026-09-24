// Command demo exercises the content negotiator end to end.
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strings"

	"ontology/mtype"
	"ontology/negotiate"
)

var fails int

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		fails++
	}
	fmt.Printf("%s: %s\n", status, name)
}

func sel(accept string, offered ...string) (string, error) {
	return negotiate.Select(&accept, offered)
}

func unacc(err error) bool { return errors.Is(err, negotiate.ErrUnacceptable) }

func badKind(in string, want mtype.Kind) bool {
	_, err := sel("ok/ok, "+in, "ok/ok")
	var pe *mtype.Error
	return errors.As(err, &pe) && pe.Kind == want && pe.Index == 1
}

func main() {
	g, _ := sel("*/*;q=0.8, text/*;q=0.8, text/plain;q=0.8", "image/png", "text/html", "text/plain")
	g2, _ := sel("text/plain;q=0.8, text/*;q=0.8, */*;q=0.8", "image/png", "text/html", "text/plain")
	check("specificity order", g == "text/plain" && g2 == "text/plain")
	g, _ = sel("text/*;q=0.9, text/plain;q=0.1", "text/html", "text/plain")
	check("q beats specificity", g == "text/html")
	_, err := sel("text/plain;q=0", "text/plain")
	check("q=0 rejects", unacc(err))
	_, err = sel("text/plain;q=1.5", "text/plain")
	var pe *mtype.Error
	check("bad q errors", errors.As(err, &pe) && pe.Kind == mtype.ErrBadQ)
	g, _ = sel("text/plain;format=flowed", "text/plain;format=flowed")
	_, e2 := sel("text/plain", "text/plain;format=flowed")
	check("param set match", g == "text/plain;format=flowed" && unacc(e2))
	_, err = sel("text/plain;format=FLOWED", "text/plain;format=flowed")
	check("param value case", unacc(err))
	g, _ = sel("text/a, text/b", "text/b", "text/a")
	check("tie: server order", g == "text/b")
	g, _ = sel("text/a, text/b", "text/a", "text/b")
	g2, _ = sel("text/b, text/a", "text/a", "text/b")
	check("accept order free", g == "text/a" && g2 == "text/a")
	g, err = negotiate.Select(nil, []string{"text/plain"})
	_, e2 = sel("", "text/plain")
	check("missing vs empty", err == nil && g == "text/plain" && unacc(e2))
	_, err = sel("image/png", "text/plain")
	check("no fallback", unacc(err))
	g, err = sel(`text/plain;x="a;b"`, `text/plain;x="a;b"`)
	check("quoted semicolon", err == nil && g == `text/plain;x="a;b"`)
	kinds := badKind("text", mtype.ErrNoSlash) && badKind("text/", mtype.ErrEmptySubtype) &&
		badKind("text/plain;fmt", mtype.ErrParamNoEq) && badKind(`text/plain;x="a`, mtype.ErrUnclosedQuote) &&
		badKind("text/plain;q=0.1234", mtype.ErrBadQ)
	check("error kinds distinct", kinds)
	base := []string{"text/a;q=0.5", "text/b;q=0.9", "text/c;q=0.7", "text/d;q=0.6"}
	want, _ := sel(strings.Join(base, ", "), "text/a", "text/b", "text/c", "text/d")
	stable := want == "text/b"
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 50 && stable; i++ {
		r.Shuffle(len(base), func(x, y int) { base[x], base[y] = base[y], base[x] })
		g, _ = sel(strings.Join(base, ", "), "text/a", "text/b", "text/c", "text/d")
		stable = g == want
	}
	check("50 shuffles stable", stable)
	fmt.Printf("total: 13 checks, %d failed\n", fails)
	if fails > 0 {
		os.Exit(1)
	}
}
