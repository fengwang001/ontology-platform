package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"ontology/api"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, name)
}

func naive(text, pat string) []int {
	var out []int
	for i := 0; i+len(pat) <= len(text); i++ {
		if text[i:i+len(pat)] == pat {
			out = append(out, i)
		}
	}
	return out
}

func search(text, pat string) ([]int, error) {
	m, err := api.New([]byte(pat))
	if err != nil {
		return nil, err
	}
	return m.Search([]byte(text))
}

func main() {
	for _, c := range []struct {
		text, pat string
		want      []int
	}{
		{"abababcab", "abab", []int{0, 2}},
		{"aaaaab", "aaaab", []int{1}},
		{"aaaa", "aa", []int{0, 1, 2}},
	} {
		got, err := search(c.text, c.pat)
		check(fmt.Sprintf("%q/%q -> %v", c.text, c.pat, c.want), err == nil && slices.Equal(got, c.want))
	}
	rng, same := rand.New(rand.NewSource(1)), true
	for i := 0; i < 200 && same; i++ {
		text, pat := make([]byte, rng.Intn(50)), make([]byte, 1+rng.Intn(6))
		for j := range text {
			text[j] = "abc"[rng.Intn(3)]
		}
		for j := range pat {
			pat[j] = "abc"[rng.Intn(3)]
		}
		got, err := search(string(text), string(pat))
		same = err == nil && slices.Equal(got, naive(string(text), string(pat)))
	}
	check("consistent with naive on 200 random cases", same)
	_, e1 := api.New(nil)
	_, e2 := api.New(make([]byte, 100000))
	m, _ := api.New([]byte("aa"))
	_, e3 := m.Search([]byte(strings.Repeat("a", 70000)))
	check("three distinct sentinel errors", errors.Is(e1, api.ErrEmptyPattern) &&
		errors.Is(e2, api.ErrPatternTooLong) && errors.Is(e3, api.ErrTooManyMatches) &&
		e1 != e2 && e2 != e3 && e1 != e3)
	m2, _ := api.New([]byte("aa"))
	m2.Search([]byte("aaaa"))
	before := m2.Matches()
	m2.Search([]byte(strings.Repeat("a", 70000)))
	check("state unchanged after rejection", slices.Equal(m2.Matches(), before))
	big, err := search(strings.Repeat("z", 10000), "abcdefghij")
	check("large-n search (sublinear cmp asserted in bm test)", err == nil && len(big) == 0)
	m3, _ := api.New([]byte("abab"))
	m3.Search([]byte("abababcab"))
	want := m3.Matches()
	var bad atomic.Int32
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if !slices.Equal(m3.Matches(), want) {
					bad.Add(1)
				}
			}
			if m3.SelfCheck() != nil {
				bad.Add(1)
			}
		}()
	}
	wg.Wait()
	check("concurrent read-only consistent", bad.Load() == 0)
	if failed {
		os.Exit(1)
	}
}
