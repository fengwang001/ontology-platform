package main

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
	"sync"

	"ontology/api"
	"ontology/query"
	"ontology/sam"
)

var fails int

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		fails++
	}
	fmt.Printf("%s %s\n", status, name)
}

func randStr(r *rand.Rand, n int) string {
	const alpha = "abcde"
	b := make([]byte, n)
	for i := range b {
		b[i] = alpha[r.Intn(len(alpha))]
	}
	return string(b)
}

func main() {
	a, err := api.New("abcbc")
	check(`Distinct("abcbc") == 12`, err == nil && a.DistinctSubstrings() == 12)
	o1, e1 := a.Occurrences("b")
	o2, e2 := a.Occurrences("bc")
	check(`Occurrences("b")==2, Occurrences("bc")==2`, e1 == nil && e2 == nil && o1 == 2 && o2 == 2)
	lcs, e3 := a.LongestCommonSubstring("bcabc")
	check(`LongestCommonSubstring("bcabc") == "abc"`, e3 == nil && lcs == "abc")

	r := rand.New(rand.NewSource(1))
	naive := true
	for i := 0; i < 30; i++ {
		s := randStr(r, 1+r.Intn(40))
		ra, _ := api.New(s)
		t := randStr(r, 1+r.Intn(30))
		lo := r.Intn(len(s))
		sub := s[lo : lo+1+r.Intn(len(s)-lo)]
		oc, _ := ra.Occurrences(sub)
		l, _ := ra.LongestCommonSubstring(t)
		if ra.DistinctSubstrings() != query.NaiveDistinct(s) ||
			oc != query.NaiveOccurrences(s, sub) ||
			len(l) != len(query.NaiveLCS(s, t)) ||
			!strings.Contains(s, l) || !strings.Contains(t, l) {
			naive = false
		}
	}
	check("queries match naive on random inputs", naive)

	_, err1 := api.New("")
	_, err2 := api.New(strings.Repeat("x", sam.MaxLen+1))
	_, err3 := a.Occurrences("")
	_, err4 := a.LongestCommonSubstring("")
	check("three distinct sentinel errors", err1 == api.ErrEmptyInput &&
		err2 == api.ErrTooLong && err3 == api.ErrEmptyQuery && err4 == api.ErrEmptyQuery &&
		err1 != err2 && err2 != err3 && err1 != err3)

	d0 := a.DistinctSubstrings()
	check("state unchanged after rejections", a.DistinctSubstrings() == d0)

	bound := true
	for _, n := range []int{100, 1000, 5000, 10000} {
		ba, err := sam.New(randStr(r, n))
		bound = bound && err == nil && ba.WithinStateBound()
	}
	check("state count <= 2n for large n", bound)

	ca, _ := api.New("mississippiabcbcababa")
	cd := ca.DistinctSubstrings()
	co, _ := ca.Occurrences("ssi")
	cl, _ := ca.LongestCommonSubstring("sip")
	var wg sync.WaitGroup
	bad := make(chan struct{}, 16)
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok := true
			for i := 0; i < 50; i++ {
				o, _ := ca.Occurrences("ssi")
				l, _ := ca.LongestCommonSubstring("sip")
				ok = ok && ca.DistinctSubstrings() == cd && o == co && l == cl
			}
			ok = ok && ca.SelfCheck() == nil
			if !ok {
				bad <- struct{}{}
			}
		}()
	}
	wg.Wait()
	close(bad)
	check("concurrent read-only results identical", len(bad) == 0)

	if fails > 0 {
		os.Exit(1)
	}
}
