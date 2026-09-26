package api_test

import (
	"errors"
	"math/rand"
	"strings"
	"sync"
	"testing"

	"ontology/api"
	"ontology/bm"
	"ontology/shift"
)

func naive(text, pat string) []int {
	var h []int
	for s := 0; s+len(pat) <= len(text); s++ {
		if strings.HasPrefix(text[s:], pat) {
			h = append(h, s)
		}
	}
	return h
}

func eq(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestBadCharTable(t *testing.T) {
	for _, pat := range []string{"", "a", "abab", "aaaab", "mississippi"} {
		tab := shift.Build(pat)
		for c := 0; c < 256; c++ {
			if w := strings.LastIndexByte(pat, byte(c)); w != tab.Last[c] {
				t.Errorf("%q: last[%q]=%d want %d", pat, byte(c), tab.Last[c], w)
			}
		}
	}
}

func TestGoodSuffixTable(t *testing.T) {
	// Crochemore gs values indexed by mismatch j; Good[j+1] must equal these.
	want := map[string][]int{
		"abab":   {2, 2, 4, 1},
		"abc":    {3, 3, 1},
		"aaaa":   {1, 2, 3, 4},
		"ababab": {2, 2, 4, 4, 6, 1},
		"issi":   {3, 3, 3, 1},
	}
	for pat, ref := range want {
		tab := shift.Build(pat)
		for j, v := range ref {
			if tab.Good[j+1] != v || tab.Good[j+1] < 1 {
				t.Errorf("%q: gs[%d]=%d want %d", pat, j+1, tab.Good[j+1], v)
			}
		}
	}
}

func TestSearchNaive(t *testing.T) {
	for _, c := range []struct{ text, pat string }{
		{"abababcab", "abab"}, {"aaaaab", "aaaab"}, {"aaaa", "aa"},
		{"ababab", "abab"}, {"", "x"}, {"x", "x"},
		{"aaaaa", "a"}, {"mississippi", "issi"}, {"abababab", "aba"},
	} {
		if got := bm.Search(c.text, c.pat); !eq(got, naive(c.text, c.pat)) {
			t.Errorf("%q in %q: got %v want %v", c.pat, c.text, got, naive(c.text, c.pat))
		}
	}
	r := rand.New(rand.NewSource(765))
	for iter := 0; iter < 500; iter++ {
		tb, pb := make([]byte, r.Intn(60)), make([]byte, 1+r.Intn(8))
		for i := range tb {
			tb[i] = "ab"[r.Intn(2)]
		}
		for i := range pb {
			pb[i] = "ab"[r.Intn(2)]
		}
		if got := bm.Search(string(tb), string(pb)); !eq(got, naive(string(tb), string(pb))) {
			t.Fatalf("pat=%q text=%q got %v", pb, tb, got)
		}
	}
}

func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	if _, err := api.New(nil); !errors.Is(err, api.ErrEmptyPattern) {
		t.Fatalf("empty: %v", err)
	}
	if _, err := api.New(make([]byte, 1<<20+1)); !errors.Is(err, api.ErrPatternTooLong) {
		t.Fatalf("long: %v", err)
	}
	if api.ErrEmptyPattern == api.ErrPatternTooLong || api.ErrPatternTooLong == api.ErrTooManyMatches ||
		api.ErrEmptyPattern == api.ErrTooManyMatches {
		t.Fatal("sentinel errors must be distinct")
	}
	q, _ := api.New([]byte("a"))
	if _, err := q.Search([]byte("aa")); err != nil {
		t.Fatal(err)
	}
	before := q.Matches()
	if _, err := q.Search([]byte(strings.Repeat("a", 1<<11))); !errors.Is(err, api.ErrTooManyMatches) {
		t.Fatalf("over limit: %v", err)
	}
	if !eq(q.Matches(), before) {
		t.Fatal("rejected call changed state")
	}
	if got, err := q.Search([]byte("ba")); err != nil || !eq(got, []int{1}) || !q.SelfCheck() {
		t.Fatalf("not usable after rejection: %v %v selfcheck=%v", got, err, q.SelfCheck())
	}
}

func TestConcurrentReaders(t *testing.T) {
	q, _ := api.New([]byte("aba"))
	if _, err := q.Search([]byte("abababababa")); err != nil {
		t.Fatal(err)
	}
	const N = 32
	base := q.Matches()
	var wg sync.WaitGroup
	bad := make(chan bool, 1)
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 100; k++ {
				if !eq(q.Matches(), base) || !q.SelfCheck() {
					select {
					case bad <- true:
					default:
					}
					return
				}
			}
		}()
	}
	wg.Wait()
	select {
	case <-bad:
		t.Fatal("a reader diverged")
	default:
	}
}
