// demo 逐条验证编辑距离与对齐脚本的语义。
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"ontology/align"
	"ontology/dist"
)

var failed bool

func check(name string, ok bool) {
	mark := "OK"
	if !ok {
		mark = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", mark, name)
}

func main() {
	cases := []struct {
		a, b string
		want int
	}{
		{"CA", "AC", 1}, {"CA", "ABC", 2}, {"ab", "ba", 1},
		{"abc", "ca", 2}, {"", "abc", 3}, {"abc", "", 3}, {"", "", 0},
	}
	ok := true
	for _, c := range cases {
		d, err := dist.Distance(c.a, c.b, 0)
		ok = ok && err == nil && d == c.want
	}
	check("section1 samples (incl CA->ABC=2)", ok)

	d1, _ := dist.Distance("é", "e", 0)
	d2, _ := dist.Distance("日本", "本日", 0)
	d3, _ := dist.Distance("\xff", "\xfe", 0)
	check("codepoints & invalid utf8", d1 == 1 && d2 == 1 && d3 == 1)

	s1, _ := dist.Distance("CA", "AC", 0)
	s2, _ := dist.Distance("AC", "CA", 0)
	zero, _ := dist.Distance("日本", "日本", 0)
	_, errLimit := dist.Distance(strings.Repeat("a", 100), strings.Repeat("b", 100), 100)
	check("symmetry, identity, limit error", s1 == s2 && zero == 0 &&
		errors.Is(errLimit, dist.ErrLimitExceeded))

	ok = true
	for _, c := range cases {
		sc, err := align.Script(c.a, c.b, 0)
		out, errA := align.Apply(c.a, sc)
		ok = ok && err == nil && errA == nil && out == c.b
		d, _ := dist.Distance(c.a, c.b, 0)
		ok = ok && len(sc) == d
	}
	check("script len == dist, apply == target", ok)

	base, _ := align.Script("kitten", "sitting", 0)
	ok = true
	for k := 0; k < 100; k++ {
		sc, _ := align.Script("kitten", "sitting", 0)
		ok = ok && fmt.Sprint(sc) == fmt.Sprint(base)
	}
	check("deterministic x100", ok)

	ok = true
	for _, n := range []int{100, 1000} {
		x := strings.Repeat("ab", n/2)
		y := strings.Repeat("ba", n/2)
		if _, err := dist.Distance(x, y, 0); err != nil {
			ok = false
		}
		ok = ok && dist.Cells() <= 2*(n+1)*(n+1)
	}
	check("cell counter <= 2*(m+1)*(n+1)", ok)

	if failed {
		os.Exit(1)
	}
	fmt.Println("TOTAL OK")
}
