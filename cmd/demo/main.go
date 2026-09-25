package main

import (
	"errors"
	"fmt"
	"math"
	"ontology/norm"
	"ontology/par"
	"ontology/span"
	"os"
	"strings"
)

func normStr(s string, p norm.Policy) (string, *span.Map) {
	n := norm.New(norm.Options{Policy: p})
	n.Write([]byte(s))
	n.Close()
	return string(n.Output()), n.Map()
}
func sameMap(a, b *span.Map) bool {
	if a.OutLen() != b.OutLen() || a.OrigLen() != b.OrigLen() {
		return false
	}
	for o := 0; o <= a.OutLen(); o++ {
		if a.ToOrig(o) != b.ToOrig(o) {
			return false
		}
	}
	for i := 0; i <= a.OrigLen(); i++ {
		if a.ToOut(i) != b.ToOut(i) {
			return false
		}
	}
	return true
}
func main() {
	var results []bool
	add := func(name string, ok bool) {
		results = append(results, ok)
		word := "OK  "
		if !ok {
			word = "FAIL"
		}
		fmt.Printf("%s %s\n", word, name)
	}
	policies := []norm.Policy{norm.Keep, norm.EnsureOne, norm.Strip}
	mixed := "a  \r\nb\t\rc \n  \r\n\ndone  "
	out, _ := normStr("\r\r\n", norm.Keep)
	add(`\r\r\n is two line endings`, out == "\n\n")
	out, _ = normStr("a  \n   \n", norm.Keep)
	add("trailing ws + blank-only line", out == "a\n\n")
	tab := []struct {
		in   string
		want [3]string
	}{{"", [3]string{"", "\n", ""}}, {"\n", [3]string{"\n", "\n", ""}},
		{"\n\n", [3]string{"\n\n", "\n", ""}}, {"  \r\n", [3]string{"\n", "\n", ""}}}
	ok := true
	for _, r := range tab {
		for i, p := range policies {
			got, _ := normStr(r.in, p)
			ok = ok && got == r.want[i]
		}
	}
	add("policy table 4 samples x 3", ok)
	whole, wholeMap := normStr(mixed, norm.Keep)
	ok = true
	for c := 0; c <= len(mixed); c++ {
		n := norm.New(norm.Options{})
		n.Write([]byte(mixed[:c]))
		n.Write([]byte(mixed[c:]))
		n.Close()
		ok = ok && string(n.Output()) == whole && sameMap(n.Map(), wholeMap)
	}
	add("all split points identical", ok)
	ok = true
	for _, p := range policies {
		once, _ := normStr(mixed, p)
		twice, _ := normStr(once, p)
		ok = ok && once == twice
	}
	add("idempotent under 3 policies", ok)
	ok = true
	for o := 0; o <= wholeMap.OutLen(); o++ {
		ok = ok && wholeMap.ToOut(wholeMap.ToOrig(o)) == o
	}
	for i := 0; i < wholeMap.OrigLen(); i++ {
		ok = ok && wholeMap.ToOut(i) <= wholeMap.ToOut(i+1) && wholeMap.ToOrig(i) <= wholeMap.ToOrig(i+1)
	}
	add("map inverse + monotonic", ok)
	_, m1 := normStr("a  \n", norm.Keep)
	_, m2 := normStr("\r\n", norm.Keep)
	add("deleted-byte ToOut forward", m1.ToOut(1) == 1 && m1.ToOut(2) == 1 && m2.ToOut(0) == 0)
	var ne *norm.Error
	n := norm.New(norm.Options{Strict: true})
	_, err := n.Write([]byte("a\x00b"))
	ok = errors.As(err, &ne) && ne.Kind == norm.KindNUL && ne.Off == 1 && string(n.Output()) == "a"
	add("NUL strict error + kept output", ok)
	n = norm.New(norm.Options{MaxWS: 2})
	_, err = n.Write([]byte("a   b"))
	ok = errors.As(err, &ne) && ne.Kind == norm.KindWS && ne.Off == 3
	add("ws buffer overflow rejects", ok)
	ok = true
	for t := 0; t <= len(mixed); t++ {
		nn := norm.New(norm.Options{})
		nn.Write([]byte(mixed[:t]))
		nn.Close()
		want, _ := normStr(mixed[:t], norm.Keep)
		ok = ok && string(nn.Output()) == want
	}
	add("truncation traversal", ok)
	ok = true
	for k := 1; k <= 8; k++ {
		po, pm, err := par.NormalizeK([]byte(mixed), k, norm.Options{})
		ok = ok && err == nil && string(po) == whole && sameMap(pm, wholeMap)
	}
	for c := 0; c <= len(mixed); c++ {
		po, pm, err := par.Normalize([]byte(mixed), []int{c}, norm.Options{})
		ok = ok && err == nil && string(po) == whole && sameMap(pm, wholeMap)
	}
	add("par K=1..8 + all cuts identical", ok)
	var sb strings.Builder
	for i := 0; i < 100000; i++ {
		sb.WriteString([]string{"x  \r\n", "y\t\r", "z \n", "  \r\n"}[i%4])
	}
	_, bm := normStr(sb.String(), norm.Keep)
	bm.ToOrig(bm.OutLen() / 2)
	checked, runs := bm.LastChecked(), bm.Len()
	bound := 2*int(math.Log2(float64(runs))) + 4
	_, pm := normStr(strings.Repeat("q\n", 5_000_000), norm.Keep)
	ok = checked <= bound && pm.Len() <= 4
	add(fmt.Sprintf("scale: checked %d<=%d, pure-LF runs %d<=4", checked, bound, pm.Len()), ok)
	bad := 0
	for _, r := range results {
		if !r {
			bad++
		}
	}
	fmt.Printf("SUMMARY %d/%d OK\n", len(results)-bad, len(results))
	if bad > 0 {
		os.Exit(1)
	}
}
