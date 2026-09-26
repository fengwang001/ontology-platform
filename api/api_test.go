package api

import (
	"errors"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

func TestQueryMatchesNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 200; iter++ {
		p := [2]string{randStr(rng, "abc", 1+rng.Intn(30)), randStr(rng, "abc", 1+rng.Intn(30))}
		m, err := New(p[0])
		if err != nil {
			t.Fatal(err)
		}
		l, st, e := m.Query(p[1])
		wl, ws := naive(p[0], p[1])
		if e != nil || l != wl || st != ws {
			t.Fatalf("(%q,%q)=(%d,%d,%v), naive=(%d,%d)", p[0], p[1], l, st, e, wl, ws)
		}
	}
}

func TestSubstringReal(t *testing.T) {
	cases := [][2]string{
		{"banana", "ananas"}, {"abcx", "abc"}, {"abcde", "abfce"},
		{"mississippi", "ssissi"}, {"abababa", "babab"},
	}
	for _, c := range cases {
		m, _ := New(c[0])
		l, st, err := m.Query(c[1])
		if err != nil {
			t.Fatal(err)
		}
		sub, err := m.Substring(c[1])
		wl, _ := naive(c[0], c[1])
		if err != nil || sub != c[0][st:st+l] || !strings.Contains(c[1], sub) || l != wl {
			t.Fatalf("(%q,%q): unreal/non-maximal substring %q l=%d st=%d max=%d",
				c[0], c[1], sub, l, st, wl)
		}
	}
}

func TestSentinelErrorsDistinct(t *testing.T) {
	long := strings.Repeat("x", maxLen)
	cases := []struct {
		a, b  string
		atNew bool
		want  error
	}{
		{"", "", true, ErrEmptyReference},
		{long + "x", "y", true, ErrInputTooLong},
		{"ok", "", false, ErrEmptyQuery},
		{"ok", long, false, ErrInputTooLong},
	}
	for _, c := range cases {
		m, err := New(c.a)
		if c.atNew {
			if !errors.Is(err, c.want) {
				t.Fatalf("New(len=%d) err=%v, want %v", len(c.a), err, c.want)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, _, e := m.Query(c.b); !errors.Is(e, c.want) {
			t.Fatalf("Query(len=%d) err=%v, want %v", len(c.b), e, c.want)
		}
	}
	if errors.Is(ErrEmptyReference, ErrEmptyQuery) ||
		errors.Is(ErrEmptyQuery, ErrInputTooLong) ||
		errors.Is(ErrEmptyReference, ErrInputTooLong) {
		t.Fatal("sentinel errors must be mutually distinct")
	}
}

func TestRejectedOpsNoTrace(t *testing.T) {
	m, _ := New("banana")
	for _, b := range []string{"", strings.Repeat("z", maxLen), ""} {
		if _, _, e := m.Query(b); e == nil {
			t.Fatalf("Query(%q) accepted", b)
		}
		if _, e := m.Substring(b); e == nil {
			t.Fatalf("Substring(%q) accepted", b)
		}
	}
	l, st, err := m.Query("ananas")
	sub, _ := m.Substring("ananas")
	if err != nil || l != 5 || st != 1 || sub != "anana" {
		t.Fatalf("after rejects: (%d,%d,%v) sub=%q", l, st, err, sub)
	}
}

func TestSelfCheck(t *testing.T) {
	m, _ := New("banana")
	if err := m.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestConcurrent(t *testing.T) {
	const N = 64
	rng := rand.New(rand.NewSource(11))
	ref := "banana" + strings.Repeat("ab", 20)
	bs := make([]string, N)
	for i := range bs {
		bs[i] = randStr(rng, "abn", 1+rng.Intn(20))
	}
	m, _ := New(ref)
	type res struct{ l, st int }
	serial := make([]res, N)
	for i, b := range bs {
		l, st, _ := m.Query(b)
		serial[i] = res{l, st}
	}
	parallel := make([]res, N)
	var wg sync.WaitGroup
	for i, b := range bs {
		wg.Add(1)
		go func(i int, b string) {
			defer wg.Done()
			l, st, e := m.Query(b)
			sub, e2 := m.Substring(b)
			if e == nil && e2 == nil && sub == ref[st:st+l] && strings.Contains(b, sub) {
				parallel[i] = res{l, st}
				return
			}
			t.Errorf("goroutine %d bad result", i)
		}(i, b)
	}
	wg.Wait()
	for i := range serial {
		if parallel[i] != serial[i] {
			t.Fatalf("b[%d]=%q: parallel=%v serial=%v", i, bs[i], parallel[i], serial[i])
		}
	}
}

func randStr(rng *rand.Rand, alpha string, k int) string {
	b := make([]byte, k)
	for i := range b {
		b[i] = alpha[rng.Intn(len(alpha))]
	}
	return string(b)
}
