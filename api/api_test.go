package api_test

import (
	"bytes"
	"errors"
	"math/rand"
	"slices"
	"sync"
	"testing"

	"ontology/api"
)

// chk asserts err against want: want==nil means success, otherwise errors.Is.
func chk(t *testing.T, err, want error) {
	t.Helper()
	if (want == nil) != (err == nil) || want != nil && !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}
func naiveSA(s []byte) []int {
	idx := make([]int, len(s))
	for i := range idx {
		idx[i] = i
	}
	return slices.SortedFunc(slices.Values(idx), func(a, b int) int { return bytes.Compare(s[a:], s[b:]) })
}
func lcpOne(s []byte, i, j int) (h int) {
	for i+h < len(s) && j+h < len(s) && s[i+h] == s[j+h] {
		h++
	}
	return
}
func naiveLongest(s []byte) (st, ln int) {
	for i := 0; i < len(s); i++ {
		for j := i + 1; j < len(s); j++ {
			if h, l := lcpOne(s, i, j), min(i, j); h > ln || (h == ln && h > 0 && l < st) {
				st, ln = l, h
			}
		}
	}
	return
}
func testCases() [][]byte {
	out := [][]byte{[]byte("ababab"), []byte("banana"), []byte("mississippi"), []byte("aaaa"), []byte("a"), []byte("abcdef"), []byte("世界世界世")}
	rng := rand.New(rand.NewSource(42))
	for range 120 {
		b := make([]byte, 1+rng.Intn(70))
		for j := range b {
			b[j] = byte('a' + rng.Intn(3))
		}
		out = append(out, b)
	}
	return out
}
func TestSAMatchesNaive(t *testing.T) {
	for _, s := range testCases() {
		var x api.Index
		chk(t, x.New(s), nil)
		got, err := x.SA()
		if err != nil || !slices.Equal(got, naiveSA(s)) {
			t.Fatalf("SA(%q) = %v, want %v", s, got, naiveSA(s))
		}
	}
}
func TestStructuralInvariants(t *testing.T) {
	for _, s := range testCases() {
		var x api.Index
		chk(t, x.New(s), nil)
		sa, _ := x.SA()
		for i, v := range slices.Sorted(slices.Values(sa)) {
			if v != i {
				t.Fatalf("SA(%q) not a permutation: %v", s, sa)
			}
		}
		lv, _ := x.LCP()
		if len(lv) != len(s)-1 {
			t.Fatalf("LCP(%q) len = %d, want %d", s, len(lv), len(s)-1)
		}
		for k := 0; k+1 < len(sa); k++ {
			if w := lcpOne(s, sa[k], sa[k+1]); lv[k] != w {
				t.Fatalf("LCP(%q)[%d] = %d, want %d", s, k, lv[k], w)
			}
		}
	}
}

func TestLongestRepeatedNaive(t *testing.T) {
	for _, c := range []struct {
		s      string
		st, ln int
	}{{"ababab", 0, 4}, {"banana", 1, 3}, {"abcdef", 0, 0}, {"aaaa", 0, 3}} {
		var x api.Index
		chk(t, x.New([]byte(c.s)), nil)
		if st, ln, _ := x.LongestRepeated(); st != c.st || ln != c.ln {
			t.Fatalf("(%q) = (%d,%d), want (%d,%d)", c.s, st, ln, c.st, c.ln)
		}
	}
	for _, s := range testCases() {
		var x api.Index
		chk(t, x.New(s), nil)
		st, ln, _ := x.LongestRepeated()
		if ws, wl := naiveLongest(s); st != ws || ln != wl {
			t.Fatalf("(%q) = (%d,%d), naive (%d,%d)", s, st, ln, ws, wl)
		}
	}
}

func TestRejectionLeavesNoTrace(t *testing.T) {
	var x api.Index
	_, err := x.SA()
	chk(t, err, api.ErrNotBuilt)
	chk(t, x.New(nil), api.ErrEmpty)
	chk(t, x.New([]byte{}), api.ErrEmpty)
	chk(t, x.New([]byte{0xff, 'a'}), api.ErrInvalidUTF8)
	_, err = x.LCP()
	chk(t, err, api.ErrNotBuilt)
	chk(t, x.New([]byte("ababab")), nil)
	for _, k := range []int{6, 100, -1} {
		_, e := x.At(k)
		chk(t, e, api.ErrOutOfRange)
	}
	got, e := x.SA()
	if e != nil || !slices.Equal(got, []int{4, 2, 0, 5, 3, 1}) {
		t.Fatalf("state changed after rejection: %v %v", got, e)
	}
}

func TestConcurrentLongestRepeated(t *testing.T) {
	var x api.Index
	chk(t, x.New(bytes.Repeat([]byte("ab"), 500)), nil)
	const N = 128
	var wg sync.WaitGroup
	starts, lens := make([]int, N), make([]int, N)
	for i := range N {
		wg.Add(1)
		go func(i int) { defer wg.Done(); starts[i], lens[i], _ = x.LongestRepeated() }(i)
	}
	wg.Wait()
	for i := 1; i < N; i++ {
		if starts[i] != starts[0] || lens[i] != lens[0] {
			t.Fatalf("goroutine %d: (%d,%d) vs (%d,%d)", i, starts[i], lens[i], starts[0], lens[0])
		}
	}
}
