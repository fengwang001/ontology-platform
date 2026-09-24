package find_test

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"ontology/find"
	"ontology/scan"
	"ontology/table"
)

func naive(text, pat string) (hits []int) {
	for i := 0; i+len(pat) <= len(text); i++ {
		if text[i:i+len(pat)] == pat {
			hits = append(hits, i)
		}
	}
	return hits
}
func TestFindAllMatchesNaive(t *testing.T) {
	cases := []struct{ text, pat string }{
		{"aaaa", "aa"}, {"abababaca", "ababaca"}, {"mississippi", "issi"}, {"abcabcabc", "abc"},
		{"xyz", "q"}, {"hello world", "o"}, {strings.Repeat("ab", 60), "aba"}, {strings.Repeat("a", 200), "aaaaa"},
	}
	for _, c := range cases {
		p, _ := find.Compile(c.pat, 1<<10)
		got, _ := p.FindAll(c.text)
		n, _ := p.Count(c.text)
		if want := naive(c.text, c.pat); !slices.Equal(got, want) || n != len(want) {
			t.Errorf("FindAll/Count(%q,%q): got %v/%d, want %v/%d", c.text, c.pat, got, n, want, len(want))
		}
	}
}
func TestTableAbabaca(t *testing.T) {
	tab := table.Compile("ababaca")
	for i, want := range []int{0, 0, 1, 2, 3, 0, 1} {
		if got := tab.At(i + 1); got != want {
			t.Errorf("At(%d)=%d, want %d", i+1, got, want)
		}
	}
}
func TestSelfCheck(t *testing.T) {
	cases := []struct{ text, pat string }{{"abababaca", "ababaca"}, {"aaaa", "aa"}, {strings.Repeat("ab", 50), "abba"}}
	for _, c := range cases {
		if p, _ := find.Compile(c.pat, 1<<10); p.SelfCheck(c.text) != nil {
			t.Errorf("SelfCheck(%q,%q) failed", c.text, c.pat)
		}
	}
}
func TestScanIsLinear(t *testing.T) {
	for _, nm := range [][2]int{{1000, 10}, {100000, 100}} {
		matcher := scan.New(table.Compile(strings.Repeat("a", nm[1]-1) + "b"))
		matcher.Scan(strings.Repeat("a", nm[0]))
		if adv, cmp := matcher.Stats(); adv != int64(nm[0]) || cmp > 2*int64(nm[0]) {
			t.Errorf("n=%d,m=%d: advances=%d comparisons=%d", nm[0], nm[1], adv, cmp)
		}
	}
}
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	p, _ := find.Compile("aa", 10)
	_, errEmpty := find.Compile("", 10)
	_, errBig := find.Compile("abcdef", 3)
	_, errLong := p.FindAll("a")
	wants := []error{find.ErrEmptyPattern, find.ErrPatternTooBig, find.ErrPatternTooLong}
	for i, err := range []error{errEmpty, errBig, errLong} {
		foreign := errors.Is(err, wants[(i+1)%3]) || errors.Is(err, wants[(i+2)%3])
		if !errors.Is(err, wants[i]) || foreign {
			t.Errorf("error %d mismatched or not distinct: %v", i, err)
		}
	}
	before, _ := p.FindAll("aaaa")
	_, _ = p.FindAll("a")
	_, _ = find.Compile("", 10)
	_, _ = find.Compile("toobig", 3)
	if after, _ := p.FindAll("aaaa"); !slices.Equal(before, after) {
		t.Errorf("rejected ops changed results: %v -> %v", before, after)
	}
}
func TestConcurrentFindAll(t *testing.T) {
	text := strings.Repeat("ab", 500) + "a"
	p, _ := find.Compile("aba", 1<<10)
	want, _ := p.FindAll(text)
	results := make([][]int, 32)
	var wg sync.WaitGroup
	for g := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, _ := p.FindAll(text)
			results[g] = got
		}()
	}
	wg.Wait()
	if slices.ContainsFunc(results, func(r []int) bool { return !slices.Equal(r, want) }) {
		t.Error("concurrent results differ")
	}
}
