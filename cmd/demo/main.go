// Command demo prints OK/FAIL lines for each guaranteed property.
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"slices"
	"strings"
	"sync"

	"ontology/find"
	"ontology/scan"
	"ontology/table"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Printf("%s %s\n", map[bool]string{true: "OK", false: "FAIL"}[ok], name)
}

func naive(text, pat string) (hits []int) {
	for i := 0; i+len(pat) <= len(text); i++ {
		if text[i:i+len(pat)] == pat {
			hits = append(hits, i)
		}
	}
	return hits
}

func main() {
	tab := table.Compile("ababaca")
	tableOK := tab.Len() == 7
	for i, want := range []int{0, 0, 1, 2, 3, 0, 1} {
		tableOK = tableOK && tab.At(i+1) == want
	}
	check("failure table of ababaca is 0 0 1 2 3 0 1", tableOK)
	hits := scan.New(tab).Scan("abababaca")
	check("ababaca in abababaca matches at [2]", slices.Equal(hits, []int{2}))
	linearOK := true
	for _, nm := range [][2]int{{1000, 10}, {100000, 100}} {
		matcher := scan.New(table.Compile(strings.Repeat("a", nm[1]-1) + "b"))
		matcher.Scan(strings.Repeat("a", nm[0]))
		adv, cmp := matcher.Stats()
		linearOK = linearOK && adv == int64(nm[0]) && cmp <= 2*int64(nm[0])
	}
	check("text advances == n and comparisons <= 2n at both scales", linearOK)
	rng, buf := rand.New(rand.NewSource(1)), make([]byte, 2000)
	for i := range buf {
		buf[i] = "ab"[rng.Intn(2)]
	}
	text := string(buf)
	p, _ := find.Compile("abba", 1<<10)
	got, err := p.FindAll(text)
	check("FindAll equals naive on random text", err == nil && slices.Equal(got, naive(text, "abba")))
	p2, _ := find.Compile("aa", 1<<10)
	p3, _ := find.Compile("ababaca", 1<<10)
	overlap, _ := p2.FindAll("aaaa")
	check("aa in aaaa finds overlaps [0 1 2]", slices.Equal(overlap, []int{0, 1, 2}))
	check("SelfCheck verifies table and hits", p.SelfCheck(text) == nil && p3.SelfCheck("abababaca") == nil)
	_, errEmpty := find.Compile("", 1<<10)
	_, errBig := find.Compile("abcdef", 3)
	_, errLong := p2.FindAll("a")
	distinct := !errors.Is(errEmpty, find.ErrPatternTooBig) &&
		!errors.Is(errBig, find.ErrPatternTooLong) && !errors.Is(errLong, find.ErrEmptyPattern)
	check("three distinct sentinel errors", errors.Is(errEmpty, find.ErrEmptyPattern) &&
		errors.Is(errBig, find.ErrPatternTooBig) && errors.Is(errLong, find.ErrPatternTooLong) && distinct)
	before, _ := p2.FindAll("aaaa")
	_, _ = p2.FindAll("a")
	_, _ = find.Compile("", 1<<10)
	_, _ = find.Compile("toolongpattern", 3)
	after, _ := p2.FindAll("aaaa")
	check("rejected ops leave compiled pattern unchanged", slices.Equal(before, after))
	results := make([][]int, 16)
	var wg sync.WaitGroup
	for g := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r, err := p.FindAll(text); err == nil {
				results[g] = r
			}
		}()
	}
	wg.Wait()
	concurrentOK := true
	for _, r := range results {
		concurrentOK = concurrentOK && slices.Equal(r, got)
	}
	check("concurrent FindAll results identical", concurrentOK)
	if failed {
		os.Exit(1)
	}
}
