package api_test

import (
	"errors"
	"math/rand"
	"strings"
	"sync"
	"testing"

	"ontology/api"
	"ontology/query"
	"ontology/sam"
)

func randStr(r *rand.Rand, n int) string {
	const alpha = "abc"
	b := make([]byte, n)
	for i := range b {
		b[i] = alpha[r.Intn(len(alpha))]
	}
	return string(b)
}

func TestAgainstNaive(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	strs := []string{"abcbc", "aaaa", "x", "ab", "mississippi", "abcabcab"}
	for i := 0; i < 12; i++ {
		strs = append(strs, randStr(r, 1+r.Intn(40)))
	}
	for _, s := range strs {
		a, err := api.New(s)
		if err != nil {
			t.Fatalf("New(%q): %v", s, err)
		}
		if got, want := a.DistinctSubstrings(), query.NaiveDistinct(s); got != want {
			t.Errorf("s=%q: distinct=%d, want %d", s, got, want)
		}
		subs := []string{s + "!"} // guaranteed missing
		for i := 0; i < len(s); i++ {
			for j := i + 1; j <= len(s); j++ {
				subs = append(subs, s[i:j])
			}
		}
		for _, sub := range subs {
			got, err := a.Occurrences(sub)
			if want := query.NaiveOccurrences(s, sub); err != nil || got != want {
				t.Errorf("s=%q sub=%q: occ=%d,%v want %d", s, sub, got, err, want)
			}
		}
		for _, tt := range []string{s, randStr(r, 1+r.Intn(25)), randStr(r, 1+r.Intn(25))} {
			got, err := a.LongestCommonSubstring(tt)
			want := query.NaiveLCS(s, tt)
			if err != nil || len(got) != len(want) ||
				!strings.Contains(s, got) || !strings.Contains(tt, got) {
				t.Errorf("s=%q t=%q: lcs=%q,%v want len %d", s, tt, got, err, len(want))
			}
		}
	}
}

func TestOccurrencesExact(t *testing.T) {
	a, err := api.New("abcbc")
	if err != nil {
		t.Fatal(err)
	}
	subs := []string{"a", "b", "c", "bc", "abc", "bcb", "cbc", "abcbc", "d", "abcbcd"}
	wants := []int{1, 2, 2, 2, 1, 1, 1, 1, 0, 0}
	for i, sub := range subs {
		got, err := a.Occurrences(sub)
		if err != nil || got != wants[i] {
			t.Errorf("Occurrences(%q) = %d, %v; want %d", sub, got, err, wants[i])
		}
	}
}

func TestSentinelErrors(t *testing.T) {
	a, _ := api.New("abcbc")
	_, e1 := api.New("")
	_, e2 := api.New(strings.Repeat("a", sam.MaxLen+1))
	_, e3 := a.Occurrences("")
	_, e4 := a.LongestCommonSubstring("")
	cases := []struct{ err, want error }{
		{e1, api.ErrEmptyInput}, {e2, api.ErrTooLong},
		{e3, api.ErrEmptyQuery}, {e4, api.ErrEmptyQuery},
	}
	for _, c := range cases {
		if !errors.Is(c.err, c.want) {
			t.Errorf("err=%v, want %v", c.err, c.want)
		}
	}
	if api.ErrEmptyInput == api.ErrTooLong || api.ErrTooLong == api.ErrEmptyQuery ||
		api.ErrEmptyInput == api.ErrEmptyQuery {
		t.Error("sentinel errors must be mutually distinct")
	}
}

func TestFailureLeavesNoTrace(t *testing.T) {
	a, err := api.New("abcbc")
	if err != nil {
		t.Fatal(err)
	}
	d0 := a.DistinctSubstrings()
	o0, _ := a.Occurrences("b")
	l0, _ := a.LongestCommonSubstring("bcabc")
	_, _ = api.New("")
	_, _ = api.New(strings.Repeat("a", sam.MaxLen+1))
	_, _ = a.Occurrences("")
	_, _ = a.LongestCommonSubstring("")
	o1, _ := a.Occurrences("b")
	l1, _ := a.LongestCommonSubstring("bcabc")
	if a.DistinctSubstrings() != d0 || o1 != o0 || l1 != l0 {
		t.Error("rejected operations changed observable state")
	}
}

func TestConcurrentReads(t *testing.T) {
	a, err := api.New("mississippiabcbcababa")
	if err != nil {
		t.Fatal(err)
	}
	d := a.DistinctSubstrings()
	o, _ := a.Occurrences("ssi")
	l, _ := a.LongestCommonSubstring("sip")
	var wg sync.WaitGroup
	bad := make(chan string, 16)
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				og, _ := a.Occurrences("ssi")
				lg, _ := a.LongestCommonSubstring("sip")
				if a.DistinctSubstrings() != d || og != o || lg != l {
					bad <- "mismatch"
					return
				}
			}
			if a.SelfCheck() != nil {
				bad <- "selfcheck"
			}
		}()
	}
	wg.Wait()
	close(bad)
	for msg := range bad {
		t.Error(msg)
	}
}
