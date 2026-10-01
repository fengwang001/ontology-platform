// Command demo exercises the streaming normalizer end to end.
package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strings"

	"ontology/norm"
	"ontology/par"
	"ontology/span"
)

var failed int

func check(name string, ok bool) {
	if !ok {
		failed++
	}
	fmt.Printf("%-4s %s\n", map[bool]string{true: "OK", false: "FAIL"}[ok], name)
}

func run(s string, cfg norm.Config) (string, *span.Map) {
	n := norm.New(cfg)
	n.Write([]byte(s))
	n.Close()
	return string(n.Output()), n.Map()
}

func mapEq(a, b *span.Map) bool {
	if a.Orig != b.Orig || a.Out != b.Out || a.Appended != b.Appended {
		return false
	}
	for o := 0; o <= a.Out; o++ {
		if a.ToOrig(o) != b.ToOrig(o) {
			return false
		}
	}
	for i := 0; i <= a.Orig; i++ {
		if a.ToOut(i) != b.ToOut(i) {
			return false
		}
	}
	return true
}

func main() {
	o, _ := run("a\r\r\nb", norm.Config{})
	check(`\r\r\n is two line endings`, o == "a\n\nb")
	o, _ = run("a  \n  \nend \t\r\n", norm.Config{})
	check("trailing ws + ws-only line", o == "a\n\nend\n")
	rows := []struct{ in, p, e, c string }{
		{"", "", "", ""}, {"\n", "\n", "\n", "\n"}, {"\n\n", "\n\n", "\n", "\n"},
		{"  \r\n", "\n", "\n", "\n"}, {"a", "a", "a\n", "a"}, {"a\n\nb", "a\n\nb", "a\n\nb\n", "a\n\nb"},
	}
	ok := true
	for _, r := range rows {
		p, _ := run(r.in, norm.Config{Policy: norm.Preserve})
		e, _ := run(r.in, norm.Config{Policy: norm.EnsureOne})
		c, _ := run(r.in, norm.Config{Policy: norm.Collapse})
		ok = ok && p == r.p && e == r.e && c == r.c
	}
	check("policy table (DESIGN.md)", ok)
	mix := "a  \r\nb\rc  \n\n\r\nd  "
	whole, wm := run(mix, norm.Config{Policy: norm.EnsureOne})
	ok = true
	for i := 0; i <= len(mix); i++ {
		n := norm.New(norm.Config{Policy: norm.EnsureOne})
		n.Write([]byte(mix[:i]))
		n.Write([]byte(mix[i:]))
		n.Close()
		ok = ok && string(n.Output()) == whole && mapEq(n.Map(), wm)
	}
	check("all split points identical", ok)
	ok = true
	for _, pol := range []norm.Policy{norm.Preserve, norm.EnsureOne, norm.Collapse} {
		x1, _ := run(mix, norm.Config{Policy: pol})
		x2, _ := run(x1, norm.Config{Policy: pol})
		ok = ok && x1 == x2
	}
	check("idempotent under 3 policies", ok)
	_, m := run("a  \r\nb\rc\n\n", norm.Config{Policy: norm.EnsureOne})
	ok = true
	for x := 0; x < m.Out; x++ {
		ok = ok && m.ToOut(m.ToOrig(x)) == x
	}
	for x := 1; x <= m.Out; x++ {
		ok = ok && m.ToOrig(x) >= m.ToOrig(x-1)
	}
	for i := 1; i <= m.Orig; i++ {
		ok = ok && m.ToOut(i) >= m.ToOut(i-1)
	}
	check("map inverse + monotone", ok)
	_, m = run("a  \n", norm.Config{})
	check("deleted ws ToOut lands before newline", m.ToOut(1) == 1 && m.ToOut(2) == 1)
	nr := norm.New(norm.Config{Strict: true})
	_, err := nr.Write([]byte("ab\x00c"))
	_, err2 := nr.Write([]byte("x"))
	var ne *norm.Error
	check("NUL strict + terminal", errors.Is(err, norm.ErrNUL) && errors.As(err, &ne) && ne.Orig == 2 && errors.Is(err2, norm.ErrClosed))
	nr = norm.New(norm.Config{WSLimit: 2})
	_, err = nr.Write([]byte("a   \n"))
	check("ws buffer limit rejects", errors.Is(err, norm.ErrWSLimit) && errors.As(err, &ne) && ne.Orig == 3)
	ok = true
	for i := 0; i <= len(mix); i++ {
		n := norm.New(norm.Config{Policy: norm.EnsureOne})
		for j := 0; j < i; j++ {
			n.Write([]byte{mix[j]})
		}
		n.Close()
		want, _ := run(mix[:i], norm.Config{Policy: norm.EnsureOne})
		ok = ok && string(n.Output()) == want
	}
	check("truncation scan", ok)
	ok = true
	for k := 1; k <= 8 && ok; k++ {
		for c := 0; c <= len(mix); c++ {
			cuts := []int{}
			for j := 1; j < k; j++ {
				cuts = append(cuts, c*j/(k-1))
			}
			po, pm, err := par.Normalize([]byte(mix), cuts, norm.Config{Policy: norm.EnsureOne})
			ok = ok && err == nil && string(po) == whole && mapEq(pm, wm)
		}
	}
	check("par all K=1..8 and cuts", ok)
	big := strings.Repeat("x  \r\n", 100000)
	_, m = run(big, norm.Config{})
	probe := 0
	for x := 0; x <= m.Out; x += 97 {
		m.ToOrig(x)
		probe = max(probe, m.Probe())
	}
	bound := 2*int(math.Log2(float64(m.Recs()))) + 4
	_, m2 := run(strings.Repeat("y  \tz\r", 1000000), norm.Config{})
	_, m3 := run(strings.Repeat("y\n", 5000000), norm.Config{})
	check("probe<=2log2+4, recs~deletions", probe <= bound && m2.Recs() <= 2000000 && m3.Recs() <= 4)
	fmt.Printf("total: %d failure(s)\n", failed)
	if failed > 0 {
		os.Exit(1)
	}
}
