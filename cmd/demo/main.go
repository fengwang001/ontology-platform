// Command demo 逐项验证查找表的行为并打印 OK/FAIL。
package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"

	"ontology/api"
	"ontology/fold"
	"ontology/keymap"
)

var failed bool

func check(name string, ok bool) {
	s := "OK"
	if !ok {
		s, failed = "FAIL", true
	}
	fmt.Printf("%s %s\n", s, name)
}

func build(n int) string {
	unit := []rune("aAéß中İ")
	r := make([]rune, n)
	for i := range r {
		r[i] = unit[i%len(unit)]
	}
	return string(r)
}

func main() {
	idem := true
	for _, s := range []string{"STRASSE", "straße", "ß", "İ", "ÀbÇ"} {
		idem = idem && fold.Fold(fold.Fold(s)) == fold.Fold(s)
	}
	check("fold idempotent", idem)
	samples := []string{"STRASSE", "strasse", "straße", "İ"}
	got := make([]string, 4)
	for i, s := range samples {
		got[i] = fold.Fold(s)
	}
	check(fmt.Sprintf("samples -> %q %q %q %q", got[0], got[1], got[2], got[3]),
		slices.Equal(got, []string{"strasse", "strasse", "straße", "İ"}))
	countOK := true
	for _, n := range []int{1000, 100000} {
		_, c := fold.FoldCount(build(n))
		countOK = countOK && c == n
	}
	check("rune counts 1000/100000", countOK)
	m := keymap.New(4, 2)
	errEmpty := m.Put("", "x")
	m.Put("ab", "1")
	m.Put("cd", "2")
	errLong, errFull := m.Put("abcde", "x"), m.Put("ef", "x")
	check("keymap 3 distinct errors", errors.Is(errEmpty, keymap.ErrEmptyKey) &&
		errors.Is(errLong, keymap.ErrKeyTooLong) && errors.Is(errFull, keymap.ErrTooManyEntries) &&
		errEmpty != errLong && errLong != errFull)
	check("keymap intact after rejects", len(m.Keys()) == 2 &&
		m.SelfCheck() == nil && m.Put("AB", "9") == nil)
	api.Init(64, 100)
	api.Put("Beta", "1")
	api.Put("alpha", "2")
	api.Put("GAMMA", "3")
	v1, ok1 := api.Get("BETA")
	v2, ok2 := api.Get("beta")
	check("case-insensitive hit", ok1 && ok2 && v1 == "1" && v2 == "1")
	api.Put("ALPHA", "9")
	want := []string{"alpha", "Beta", "GAMMA"}
	check("keys keep first spelling", slices.Equal(api.Keys(), want))
	api.Init(64, 100)
	for _, k := range []string{"GAMMA", "alpha", "Beta"} {
		api.Put(k, "x")
	}
	check("keys order deterministic", slices.Equal(api.Keys(), want) && api.SelfCheck() == nil)
	start, results := make(chan struct{}), make(chan []string, 64)
	var wg sync.WaitGroup
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- api.Keys()
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	concOK := true
	for r := range results {
		concOK = concOK && slices.Equal(r, want)
	}
	check("concurrent reads consistent", concOK)
	if failed {
		os.Exit(1)
	}
}
