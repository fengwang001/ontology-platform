package bm

import (
	"strings"
	"testing"
)

// The bad-character rule must skip m positions when the text's rightmost
// window character is absent from the pattern, so the number of compared
// character pairs stays ~n/m instead of growing linearly with n.
func TestCompareCountSublinear(t *testing.T) {
	pat := []byte("abcdefghij") // distinct bytes, last one absent from text
	m := len(pat)
	for _, n := range []int{100, 500, 1000, 5000, 10000} {
		s := New(pat)
		got, over := s.Search([]byte(strings.Repeat("z", n)), 1<<30)
		if over || len(got) != 0 {
			t.Fatalf("n=%d: got %v, over=%v", n, got, over)
		}
		if limit := n/m + m; s.cmp > limit {
			t.Fatalf("n=%d: compared %d pairs, want <= %d", n, s.cmp, limit)
		}
	}
}

func TestSearchOverlapping(t *testing.T) {
	cases := []struct {
		text, pat string
		want      []int
	}{
		{"abababcab", "abab", []int{0, 2}},
		{"aaaaab", "aaaab", []int{1}},
		{"aaaa", "aa", []int{0, 1, 2}},
		{"ababab", "abab", []int{0, 2}},
	}
	for _, c := range cases {
		got, over := New([]byte(c.pat)).Search([]byte(c.text), 1<<30)
		if over || !equalInts(got, c.want) {
			t.Fatalf("%q/%q: got %v, want %v", c.text, c.pat, got, c.want)
		}
	}
}

func equalInts(a, b []int) bool {
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
