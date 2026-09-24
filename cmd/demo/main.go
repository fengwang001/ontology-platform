package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"

	"ontology/find"
	"ontology/scan"
	"ontology/table"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status, failed = "FAIL", true
	}
	fmt.Println(status, name)
}
func naive(t, p string) (hits []int) {
	for i := 0; i+len(p) <= len(t); i++ {
		if t[i:i+len(p)] == p {
			hits = append(hits, i)
		}
	}
	return
}
func rnd(rng *rand.Rand, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = "ab"[rng.Intn(2)]
	}
	return string(b)
}
func main() {
	tbl := table.Build("ababaca")
	row := make([]int, tbl.Len())
	for k := range row {
		row[k] = tbl.At(k)
	}
	check("ababaca table & abababaca walkthrough", slices.Equal(row, []int{0, 0, 1, 2, 3, 0, 1}) &&
		slices.Equal(scan.New("ababaca", tbl).Scan("abababaca"), []int{2}))
	rng, ok := rand.New(rand.NewSource(1)), true
	for i := 0; i < 300 && ok; i++ {
		text, pat := rnd(rng, 9+rng.Intn(72)), rnd(rng, 1+rng.Intn(9))
		m, err := find.Compile(pat, 0)
		hits, herr := m.FindAll(text)
		ok = err == nil && herr == nil && slices.Equal(hits, naive(text, pat))
	}
	check("random texts match naive", ok)
	m, _ := find.Compile("aa", 0)
	hits, _ := m.FindAll("aaaa")
	check("overlap aaaa/aa=[0 1 2]", slices.Equal(hits, []int{0, 1, 2}))
	mk, _ := find.Compile("ababaca", 0)
	check("selfcheck(ababaca, abababaca)", mk.SelfCheck("abababaca") == nil)
	g, _ := find.Compile("abc", 0)
	before, _ := g.FindAll("abcabc")
	_, e1 := find.Compile("", 0)
	_, e2 := g.FindAll("ab")
	_, e3 := find.Compile("abcdef", 3)
	ok = errors.Is(e1, find.ErrEmptyPattern) && errors.Is(e2, find.ErrPatternTooLong) &&
		errors.Is(e3, find.ErrExceedsLimit) && e1 != e2 && e2 != e3 && e1 != e3
	check("3 distinct sentinel errors", ok)
	after, _ := g.FindAll("abcabc")
	check("rejected ops keep compiled state", slices.Equal(before, after))
	ok = true
	for _, nm := range [][2]int{{1000, 10}, {100000, 100}} {
		s := scan.New(strings.Repeat("a", nm[1]-1)+"b", table.Build(strings.Repeat("a", nm[1]-1)+"b"))
		s.Scan(strings.Repeat("a", nm[0]))
		v := reflect.ValueOf(s).Elem()
		ok = ok && v.FieldByName("advances").Int() == int64(nm[0]) && v.FieldByName("compares").Int() <= int64(2*nm[0])
	}
	check("advances==n, compares<=2n (worst form)", ok)
	text := strings.Repeat("a", 500) + "b"
	want, _ := m.FindAll(text)
	res := make([][]int, 16)
	var wg sync.WaitGroup
	for w := range res {
		wg.Add(1)
		go func() { defer wg.Done(); res[w], _ = m.FindAll(text) }()
	}
	wg.Wait()
	ok = true
	for _, r := range res {
		ok = ok && slices.Equal(r, want)
	}
	check("concurrent FindAll identical", ok)
	if failed {
		os.Exit(1)
	}
}
