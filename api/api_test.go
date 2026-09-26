package api

import (
	"bytes"
	"errors"
	"math/rand"
	"slices"
	"sync"
	"testing"
)

func search(t *testing.T, text, pat string) []int {
	t.Helper()
	m, err := New([]byte(pat))
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.Search([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestSearchMatchesNaive(t *testing.T) {
	cases := []struct{ text, pat string }{
		{"abababcab", "abab"}, {"aaaaab", "aaaab"}, {"aaaa", "aa"},
		{"ababab", "abab"}, {"", "a"}, {"a", "aa"}, {"abc", "abcd"},
		{"mississippi", "ss"}, {"xyz", "z"}, {"aaa", "aaa"},
	}
	for _, c := range cases {
		if got := search(t, c.text, c.pat); !slices.Equal(got, naive([]byte(c.text), []byte(c.pat))) {
			t.Fatalf("%q/%q: got %v", c.text, c.pat, got)
		}
	}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 300; i++ {
		text, pat := make([]byte, rng.Intn(60)), make([]byte, 1+rng.Intn(8))
		for j := range text {
			text[j] = "abc"[rng.Intn(3)]
		}
		for j := range pat {
			pat[j] = "abc"[rng.Intn(3)]
		}
		if got := search(t, string(text), string(pat)); !slices.Equal(got, naive(text, pat)) {
			t.Fatalf("%q/%q: got %v, want %v", text, pat, got, naive(text, pat))
		}
	}
}

func TestExhaustiveSmall(t *testing.T) {
	var gen func(n int) [][]byte
	gen = func(n int) [][]byte {
		if n == 0 {
			return [][]byte{{}}
		}
		var out [][]byte
		for _, s := range gen(n - 1) {
			out = append(out, append(slices.Clone(s), 'a'), append(slices.Clone(s), 'b'))
		}
		return out
	}
	var pats, texts [][]byte
	for n := 1; n <= 4; n++ {
		pats = append(pats, gen(n)...)
	}
	for n := 0; n <= 8; n++ {
		texts = append(texts, gen(n)...)
	}
	for _, p := range pats {
		m, err := New(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, text := range texts {
			if got, err := m.Search(text); err != nil || !slices.Equal(got, naive(text, p)) {
				t.Fatalf("%q/%q: got %v, want %v", text, p, got, naive(text, p))
			}
		}
	}
}

func TestErrorsDistinct(t *testing.T) {
	if ErrEmptyPattern == ErrPatternTooLong || ErrPatternTooLong == ErrTooManyMatches ||
		ErrEmptyPattern == ErrTooManyMatches {
		t.Fatal("sentinel errors must be distinct")
	}
	if _, err := New(nil); !errors.Is(err, ErrEmptyPattern) {
		t.Fatalf("empty pattern: %v", err)
	}
	if _, err := New(make([]byte, maxPatLen+1)); !errors.Is(err, ErrPatternTooLong) {
		t.Fatalf("overlong pattern: %v", err)
	}
	m, _ := New([]byte("aa"))
	if _, err := m.Search(bytes.Repeat([]byte("a"), maxMatches+2)); !errors.Is(err, ErrTooManyMatches) {
		t.Fatalf("match overflow: %v", err)
	}
}

func TestFailureLeavesNoState(t *testing.T) {
	m, _ := New([]byte("aa"))
	if _, err := m.Search([]byte("aaaa")); err != nil {
		t.Fatal(err)
	}
	before := m.Matches()
	if _, err := m.Search(bytes.Repeat([]byte("a"), maxMatches+2)); !errors.Is(err, ErrTooManyMatches) {
		t.Fatalf("want ErrTooManyMatches, got %v", err)
	}
	if !slices.Equal(m.Matches(), before) {
		t.Fatal("rejected search changed state")
	}
	if got, err := m.Search([]byte("baa")); err != nil || !slices.Equal(got, []int{1}) {
		t.Fatalf("search after rejection: %v, %v", got, err)
	}
}

func TestSelfCheck(t *testing.T) {
	m, err := New([]byte("abab"))
	if err != nil || m.SelfCheck() != nil {
		t.Fatalf("new=%v selfcheck=%v", err, m.SelfCheck())
	}
}

func TestConcurrentMatches(t *testing.T) {
	m, _ := New([]byte("abab"))
	if _, err := m.Search([]byte("abababcab")); err != nil {
		t.Fatal(err)
	}
	want := m.Matches()
	start := make(chan struct{})
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 100; i++ {
				if !slices.Equal(m.Matches(), want) {
					t.Error("concurrent Matches diverged")
					return
				}
			}
			if err := m.SelfCheck(); err != nil {
				t.Error(err)
			}
		}()
	}
	close(start)
	wg.Wait()
}
