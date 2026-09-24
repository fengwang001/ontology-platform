package find

import (
	"errors"
	"math/rand"
	"slices"
	"strings"
	"sync"
	"testing"
)

func naive(t, p string) []int {
	var hits []int
	for i := 0; i+len(p) <= len(t); i++ {
		if t[i:i+len(p)] == p {
			hits = append(hits, i)
		}
	}
	return hits
}

func rnd(rng *rand.Rand, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = "ab"[rng.Intn(2)]
	}
	return string(b)
}

func TestFindAllMatchesNaive(t *testing.T) {
	cases := []struct{ text, pat string }{{"aaaa", "aa"}, {"abababaca", "ababaca"}, {"abc", "abcd"}, {"a", "a"}}
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 200; i++ {
		cases = append(cases, struct{ text, pat string }{rnd(rng, 9+rng.Intn(72)), rnd(rng, 1+rng.Intn(9))})
	}
	for _, c := range cases {
		m, _ := Compile(c.pat, 0)
		got, err := m.FindAll(c.text)
		want := naive(c.text, c.pat)
		if len(c.pat) > len(c.text) {
			if !errors.Is(err, ErrPatternTooLong) {
				t.Errorf("pat=%q text=%q: err=%v, want ErrPatternTooLong", c.pat, c.text, err)
			}
			continue
		}
		if !slices.Equal(got, want) {
			t.Errorf("pat=%q text=%q: got %v, want %v", c.pat, c.text, got, want)
		}
		if n, _ := m.Count(c.text); n != len(want) {
			t.Errorf("pat=%q text=%q: Count=%d, want %d", c.pat, c.text, n, len(want))
		}
	}
}

func TestSelfCheck(t *testing.T) {
	cases := []struct{ pat, text string }{{"ababaca", "abababaca"}, {"aa", "aaaa"}, {"a", "a"}, {"abc", "abcabc"}}
	for _, c := range cases {
		m, _ := Compile(c.pat, 0)
		if err := m.SelfCheck(c.text); err != nil {
			t.Errorf("SelfCheck(%q, %q): %v", c.pat, c.text, err)
		}
	}
}

func TestRejectedCompileKeepsState(t *testing.T) {
	m, _ := Compile("abc", 0)
	want, _ := m.FindAll("abcabc")
	_, e1 := Compile("", 0)
	_, e2 := m.FindAll("ab")
	_, e3 := Compile("abcdef", 3)
	if !errors.Is(e1, ErrEmptyPattern) || !errors.Is(e2, ErrPatternTooLong) ||
		!errors.Is(e3, ErrExceedsLimit) || e1 == e2 || e2 == e3 || e1 == e3 {
		t.Fatalf("bad sentinels: %v %v %v", e1, e2, e3)
	}
	if got, _ := m.FindAll("abcabc"); !slices.Equal(got, want) {
		t.Errorf("state polluted: got %v, want %v", got, want)
	}
}

func TestConcurrentFindAll(t *testing.T) {
	m, _ := Compile("aa", 0)
	text := strings.Repeat("a", 1000) + "b"
	want, _ := m.FindAll(text)
	res := make([][]int, 32)
	var wg sync.WaitGroup
	for i := range res {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res[i], _ = m.FindAll(text)
		}()
	}
	wg.Wait()
	for i, r := range res {
		if !slices.Equal(r, want) {
			t.Errorf("goroutine %d: got %v, want %v", i, r, want)
		}
	}
}
