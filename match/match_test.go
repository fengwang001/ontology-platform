package match

import (
	"math/rand/v2"
	"slices"
	"testing"

	"ontology/pfx"
)

func naive(p, t []byte) []int {
	var out []int
	for s := 0; s+len(p) <= len(t); s++ {
		i := 0
		for i < len(p) && t[s+i] == p[i] {
			i++
		}
		if i == len(p) {
			out = append(out, s+len(p)-1)
		}
	}
	return out
}

func randBytes(r *rand.Rand, n, alpha int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + r.IntN(alpha))
	}
	return b
}

func TestPrefixFunction(t *testing.T) {
	cases := []struct {
		p    string
		want []int
	}{
		{"abaaba", []int{0, 0, 1, 1, 2, 3}},
		{"a", []int{0}},
		{"aa", []int{0, 1}},
		{"ab", []int{0, 0}},
		{"aaa", []int{0, 1, 2}},
		{"abcabc", []int{0, 0, 0, 1, 2, 3}},
	}
	for _, c := range cases {
		if got := pfx.Compute([]byte(c.p)); !slices.Equal(got, c.want) {
			t.Errorf("pi(%q)=%v want %v", c.p, got, c.want)
		}
	}
	r := rand.New(rand.NewPCG(1, 2))
	for trial := 0; trial < 200; trial++ {
		p := randBytes(r, 1+r.IntN(12), 3)
		pi := pfx.Compute(p)
		for i := range p {
			want := 0
			for l := 1; l <= i; l++ { // longest proper prefix==suffix
				if slices.Equal(p[:l], p[i+1-l:i+1]) {
					want = l
				}
			}
			if pi[i] != want {
				t.Fatalf("pi(%q)[%d]=%d want %d", p, i, pi[i], want)
			}
		}
	}
}

func TestStateInvariant(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	for trial := 0; trial < 50; trial++ {
		p := randBytes(r, 1+r.IntN(6), 2)
		m := New(p, 1<<20)
		var consumed []byte
		for _, c := range randBytes(r, 200, 2) {
			if _, err := m.Feed([]byte{c}); err != nil {
				t.Fatal(err)
			}
			consumed = append(consumed, c)
			want := 0 // longest suffix==prefix of length < len(p)
			for l := 1; l < len(p) && l <= len(consumed); l++ {
				if slices.Equal(consumed[len(consumed)-l:], p[:l]) {
					want = l
				}
			}
			if m.j != want {
				t.Fatalf("p=%q consumed=%q: j=%d want %d", p, consumed, m.j, want)
			}
		}
	}
}

func TestNaiveConsistency(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))
	for trial := 0; trial < 100; trial++ {
		p := randBytes(r, 1+r.IntN(8), 3)
		text := randBytes(r, r.IntN(500), 3)
		for _, chunk := range []int{1, 3, 7, 64, 1000} {
			m := New(p, 1<<20)
			for k := 0; k < len(text); k += chunk {
				if _, err := m.Feed(text[k:min(k+chunk, len(text))]); err != nil {
					t.Fatal(err)
				}
			}
			if got, want := m.Hits(), naive(p, text); !slices.Equal(got, want) {
				t.Fatalf("p=%q chunk=%d: got %v want %v", p, chunk, got, want)
			}
		}
	}
}

func TestComparisonBound(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 8))
	const n = 8
	p := randBytes(r, n, 2)
	for _, size := range []int{100, 500, 1000, 5000, 10000} {
		m := New(p, 1<<20)
		text := randBytes(r, size, 2)
		if _, err := m.Feed(text); err != nil {
			t.Fatal(err)
		}
		if bound := 2 * (size + n); m.cmps > bound {
			t.Fatalf("m=%d: cmps=%d exceeds %d (grows with n*m)", size, m.cmps, bound)
		}
	}
}

func TestFeedAtomicity(t *testing.T) {
	m := New([]byte("aa"), 2)
	if _, err := m.Feed([]byte("aa")); err != nil {
		t.Fatal(err)
	}
	j, pos, cmps, hits, dropped := m.j, m.pos, m.cmps, m.Hits(), m.dropped
	if _, err := m.Feed([]byte("aaa")); err != ErrTooManyMatches {
		t.Fatalf("want ErrTooManyMatches, got %v", err)
	}
	if m.j != j || m.pos != pos || m.cmps != cmps || m.dropped != dropped ||
		!slices.Equal(m.Hits(), hits) {
		t.Fatal("rejected feed changed state")
	}
	if _, err := m.Feed([]byte("a")); err != nil || !slices.Equal(m.Hits(), []int{1, 2}) {
		t.Fatalf("matcher unusable after rejection: err=%v hits=%v", err, m.Hits())
	}
}
