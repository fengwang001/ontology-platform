// Command demo prints OK/FAIL for each semantics clause of the
// properties parser. Checks are added as packages are implemented.
package main

import (
	"fmt"
	"os"
	"strings"

	"ontology/logical"
)

var failures int

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%-24s %s\n", name, status)
}

func main() {
	checkContinuation()
	checkCounter()
	fmt.Printf("total: %d failure(s)\n", failures)
	if failures > 0 {
		os.Exit(1)
	}
}

func texts(in string) []string {
	var out []string
	for _, l := range logical.Lines(in) {
		out = append(out, l.Text)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func checkContinuation() {
	ok := equal(texts("k=v\\\n   w"), []string{"k=vw"}) &&
		equal(texts("k=v\\\\\nx=y"), []string{`k=v\\`, "x=y"}) &&
		equal(texts("k=v\\\\\\\n  w"), []string{`k=v\\w`}) &&
		equal(texts("k=a\\\n#b"), []string{"k=a#b"}) &&
		equal(texts("k=v\\"), []string{"k=v"}) &&
		equal(texts("k=v\\\n   \nx=y"), []string{"k=v", "x=y"})
	check("continuation+blankjoin", ok)
}

func checkCounter() {
	chain := strings.Repeat("k=aaaaaaaaaa\\\n bbbbbbbbbb\\\n  cccccccccc\n", 1)
	for len(chain) < 1<<20 {
		chain += chain
	}
	chain = chain[:1<<20]
	before := logical.Inspected()
	logical.Lines(chain)
	got := logical.Inspected() - before
	check("inspection<=2x", got <= 2*int64(len(chain)))
}
