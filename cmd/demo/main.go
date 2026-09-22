// Demo runs every guarantee of the URL canonicalizer as one OK/FAIL line.
package main

import (
	"fmt"
	"math/rand"
	"os"
	"strings"

	"ontology/canon"
)

var failures int

func check(name string, ok bool, detail string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%-4s %-22s %s\n", status, name, detail)
}

func main() {
	ord := canon.New(canon.ModeOrdered, canon.Limits{})
	srt := canon.New(canon.ModeSorted, canon.Limits{})
	check("idempotent-2000", idemOK(srt), "random URLs, canon(canon(x))==canon(x)")
	check("reserved-escape", mustEq(ord, "http://h/a%2Fb", "http://h/a%2fb") &&
		!mustEq(ord, "http://h/a%2Fb", "http://h/a/b"), "%2F kept, /a%2Fb != /a/b")
	check("path-resolution", canonIs(ord, "http://h/../../x", "http://h/x") &&
		!mustEq(ord, "http://h/a", "http://h/a/"), ".. swallowed at root, trailing / matters")
	check("query-modes", queryModesOK(ord, srt), "a / a= / a=%20 distinct; dup keys")
	check("escape-errors", escapeErrOK(ord), "3 kinds, offsets, no partial result")
	check("ipv6-port", mustEq(ord,
		"http://[2001:0db8:0000:0000:0000:0000:0000:0001]:080/x",
		"http://[2001:db8::1]/x"), "zero compression + port normalize")
	check("equiv-classes", equivOK(ord), "intra-group equal, cross-group distinct")
	check("scan-linear", scanOK(), "1KB vs 64KB scan counts")
	check("limits", limitsOK(), "3 limit kinds, rejection keeps state")
	check("query-stable", stableOK(srt), "two reads identical")
	check("concurrent", concurrentOK(srt), "parallel == serial")
	if failures == 0 {
		fmt.Println("TOTAL 11/11 OK")
	} else {
		fmt.Printf("TOTAL %d/11 FAILED\n", failures)
		os.Exit(1)
	}
}

func mustEq(n *canon.Normalizer, a, b string) bool {
	eq, err := n.Equivalent(a, b)
	return err == nil && eq
}

func canonIs(n *canon.Normalizer, in, want string) bool {
	r, err := n.Normalize(in)
	return err == nil && r.Canonical == want
}

func idemOK(n *canon.Normalizer) bool {
	rng := rand.New(rand.NewSource(7))
	segs := []string{"a", ".", "..", "%41", "%2f", "", "%2e%2e", "x"}
	for i := 0; i < 2000; i++ {
		var b strings.Builder
		b.WriteString("HTTP://Example.COM:080")
		for j := rng.Intn(5); j >= 0; j-- {
			b.WriteByte('/')
			b.WriteString(segs[rng.Intn(len(segs))])
		}
		if rng.Intn(2) == 0 {
			fmt.Fprintf(&b, "?a=%d&b=%%7E", rng.Intn(9))
		}
		r1, err := n.Normalize(b.String())
		if err != nil {
			continue
		}
		r2, err := n.Normalize(r1.Canonical)
		if err != nil || r1.Canonical != r2.Canonical {
			return false
		}
	}
	return true
}
