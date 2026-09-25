// Command demo exercises the diff/patch packages end to end.
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"ontology/edit"
	"ontology/patch"
	"ontology/udiff"
)

var fails int

func check(name string, ok bool) {
	if !ok {
		fails++
	}
	fmt.Println(map[bool]string{true: "OK  ", false: "FAIL"}[ok] + name)
}

func lcs(a, b []string) int {
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				dp[i][j] = max(dp[i-1][j], dp[i][j-1])
			}
		}
	}
	return dp[len(a)][len(b)]
}

func apply(a []byte, p *udiff.Patch, f int) ([]byte, bool) {
	q, err := udiff.Parse(udiff.Render(p), udiff.Limits{})
	if err != nil {
		return nil, false
	}
	out, err := patch.Apply(a, q, f)
	return out, err == nil
}

func main() {
	r := rand.New(rand.NewSource(7))
	ok := true
	for i := 0; i < 50 && ok; i++ {
		mk := func() []byte {
			var sb strings.Builder
			for n := r.Intn(10); n > 0; n-- {
				sb.WriteByte(byte('a' + r.Intn(3)))
				sb.WriteString([]string{"\n", "\r\n", ""}[r.Intn(3)])
			}
			return []byte(sb.String())
		}
		a, b := mk(), mk()
		p, _ := udiff.Diff(a, b, 2, -1)
		out, ok1 := apply(a, p, 0)
		back, _ := patch.Reverse(b, mustParse(string(udiff.Render(p))), 0)
		ok = ok1 && string(out) == string(b) && string(back) == string(a)
	}
	check("roundtrip+reverse random x50", ok)
	p, _ := udiff.Diff([]byte("a\r\nb\r\n"), []byte("a\r\nc"), 3, -1)
	out, ok := apply([]byte("a\r\nb\r\n"), p, 0)
	check("CRLF and no-EOL preserved", ok && string(out) == "a\r\nc")
	ok = true
	for i := 0; i < 200 && ok; i++ {
		mk := func() []string {
			ls := make([]string, r.Intn(8))
			for j := range ls {
				ls[j] = string(rune('a'+r.Intn(3))) + "\n"
			}
			return ls
		}
		a, b := mk(), mk()
		ops, _ := edit.Diff(a, b, -1)
		d := 0
		for _, op := range ops {
			if op.Kind != ' ' {
				d++
			}
		}
		ok = d == len(a)+len(b)-2*lcs(a, b)
	}
	check("minimality vs DP x200", ok)
	ok = true
	for _, c := range [][3]string{{"x\n", "y\nx\n", "@@ -0,0 +1 @@"}, {"a\nb\nc\n", "a\nb\nX\nc\n", "@@ -2,0 +3 @@"}, {"a\n", "", "@@ -1 +0,0 @@"}} {
		p, _ := udiff.Diff([]byte(c[0]), []byte(c[1]), 0, -1)
		ok = ok && strings.Contains(string(udiff.Render(p)), c[2])
	}
	check("zero-count hunk headers x3", ok)
	mkh := func(g int, v string) []byte {
		return []byte("A" + v + "\n" + strings.Repeat("x\n", g) + "B" + v + "\n")
	}
	p2, _ := udiff.Diff(mkh(2, ""), mkh(2, "1"), 1, -1)
	p3, _ := udiff.Diff(mkh(3, ""), mkh(3, "1"), 1, -1)
	check("merge threshold g<=2C", len(p2.Hunks) == 1 && len(p3.Hunks) == 2)
	p, _ = udiff.Diff([]byte("x\n"), []byte("x"), 3, -1)
	out, ok = apply([]byte("x\n"), p, 0)
	check("EOL-only change nonempty+applies", ok && len(p.Hunks) > 0 && string(out) == "x")
	q := mustParse("--- a\n+++ b\n@@ -3,2 +3,2 @@\n-b\n+B\n a\n")
	out, ok = apply([]byte("z\nb\na\nb\na\n"), q, 3)
	check("offset apply picks nearest/earlier", ok && string(out) == "z\nB\na\nb\na\n")
	s := patch.NewStore()
	s.Put("d", []byte("a\nb\nX\n"))
	bad := mustParse("--- a\n+++ b\n@@ -1 +1 @@\n-a\n+A\n@@ -3 +3 @@\n-c\n+C\n")
	_, err1 := s.Apply("d", bad, 0)
	got, ver := s.Get("d")
	check("atomic reject keeps state", err1 != nil && string(got) == "a\nb\nX\n" && ver == 0)
	p, _ = udiff.Diff([]byte("1\n2\n3\n4\n5\n"), []byte("1\nX\n3\n4\nY\n"), 1, -1)
	data := udiff.Render(p)
	for i := 0; i <= len(data); i++ { // any panic here fails the run
		if t, err := udiff.Parse(data[:i], udiff.Limits{}); err == nil {
			patch.Apply([]byte("1\n2\n3\n4\n5\n"), t, 0)
		}
	}
	check("truncation fuzz no panic", true)
	_, ferr := udiff.Parse([]byte("junk\n"), udiff.Limits{})
	_, terr := edit.Diff([]string{"1\n"}, []string{"2\n", "3\n"}, 1)
	cls := errors.Is(ferr, udiff.ErrFormat) && errors.Is(terr, edit.ErrTooBig) &&
		!errors.Is(ferr, patch.ErrContext) && !errors.Is(terr, udiff.ErrFormat)
	check("error classes distinguishable", cls)
	s2 := patch.NewStore()
	s2.Put("d", []byte("a\nb\nc\n"))
	var okN atomic.Int64
	var wg sync.WaitGroup
	st := make(chan struct{})
	for i := 0; i < 16; i++ {
		pc := mustParse(fmt.Sprintf("--- a\n+++ b\n@@ -2 +2 @@\n-b\n+B%d\n", i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-st
			if _, err := s2.Apply("d", pc, 0); err == nil {
				okN.Add(1)
			}
		}()
	}
	close(st)
	wg.Wait()
	cur := []byte("a\nb\nc\n")
	for _, cm := range s2.Log() {
		cur, _ = patch.Apply(cur, cm.Patch, 0)
	}
	fin, fv := s2.Get("d")
	check("concurrent == serial replay", string(fin) == string(cur) && fv == int(okN.Load()))
	var prev int64
	ok = true
	for _, n := range []int{1000, 100000} {
		a := make([]string, n)
		for i := range a {
			a[i] = fmt.Sprintf("l%d\n", i)
		}
		b := append([]string(nil), a...)
		b[n/4], b[n/2], b[3*n/4] = "c\n", "c\n", "c\n"
		edit.Diff(a, b, -1)
		st2 := edit.Steps()
		ok = ok && st2 <= 4*int64(2*n)*7 && (prev == 0 || st2 <= 150*prev)
		prev = st2
	}
	check("step counter near-linear", ok)
	fmt.Printf("TOTAL %d/12 passed\n", 12-fails)
	if fails > 0 {
		os.Exit(1)
	}
}

func mustParse(s string) *udiff.Patch {
	p, err := udiff.Parse([]byte(s), udiff.Limits{})
	if err != nil {
		panic(err)
	}
	return p
}
