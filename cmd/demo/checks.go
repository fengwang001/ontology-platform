package main

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"

	"ontology/canon"
	"ontology/pct"
)

func idempotent(n *canon.Normalizer) bool {
	r := rand.New(rand.NewSource(99))
	parts := []string{"a", "%41", "%2f", ".", "..", "%", "%GG", "/", "&",
		"=", "?", ":", "@", "[", "]", "中", "\xff", "080", "%2E", "~"}
	for i := 0; i < 5000; i++ {
		s := "http://h"
		for j, m := 0, r.Intn(10); j < m; j++ {
			s += parts[r.Intn(len(parts))]
		}
		once, err := n.Normalize(s)
		if err != nil {
			continue
		}
		twice, err := n.Normalize(once)
		if err != nil || once != twice {
			return false
		}
	}
	return true
}

func queryChecks(n, ord *canon.Normalizer) bool {
	for _, p := range [][2]string{
		{"http://h/?a", "http://h/?a="},
		{"http://h/?a=", "http://h/?a=%20"},
		{"http://h/?a", "http://h/?a=%20"},
	} {
		if eq, _ := n.Equivalent(p[0], p[1]); eq {
			return false
		}
	}
	eqO, _ := ord.Equivalent("http://h/?a=1&a=2", "http://h/?a=2&a=1")
	eqS, _ := n.Equivalent("http://h/?a=1&a=2", "http://h/?a=2&a=1")
	return !eqO && eqS
}

func escapeKinds(n *canon.Normalizer) bool {
	kinds := map[pct.ErrorKind]bool{}
	for _, in := range []string{"http://h/a%4", "http://h/a%4x", "http://h/a%FF"} {
		got, err := n.Normalize(in)
		var pe *pct.Error
		if err == nil || got != "" || !errors.As(err, &pe) {
			return false
		}
		kinds[pe.Kind] = true
	}
	return len(kinds) == 3
}

func equivClasses(n *canon.Normalizer) bool {
	groups := [][]string{
		{"http://example.com/a", "HTTP://EXAMPLE.com.:80/a",
			"http://example.com/%61", "http://example.com/x/../a"},
		{"http://example.com/a%2Fb"},
		{"http://example.com/a/"},
		{"http://[2001:db8::1]/", "http://[2001:0db8:0:0:0:0:0:1]/"},
		{"http://example.com/p?b=2&a=1", "http://example.com/p?a=1&b=2"},
	}
	for gi, g := range groups {
		for _, a := range g {
			for _, b := range g {
				if eq, _ := n.Equivalent(a, b); !eq {
					return false
				}
			}
		}
		for hi, h := range groups {
			if gi != hi {
				if eq, _ := n.Equivalent(g[0], h[0]); eq {
					return false
				}
			}
		}
	}
	return true
}

func scanPair() (int64, int64, int, int) {
	n := canon.New(canon.Config{})
	build := func(size int) string {
		var b strings.Builder
		b.WriteString("http://example.com")
		for i := 0; b.Len() < size; i++ {
			fmt.Fprintf(&b, "/s%d%%41", i)
		}
		return b.String()
	}
	small, big := build(1<<10), build(1<<16)
	b0 := n.ScanCount()
	_, _ = n.Normalize(small)
	s1 := n.ScanCount() - b0
	_, _ = n.Normalize(big)
	s2 := n.ScanCount() - b0 - s1
	return s1, s2, len(small), len(big)
}

func limits() bool {
	n := canon.New(canon.Config{MaxLength: 32, MaxSegments: 3, MaxParams: 2})
	_, e1 := n.Normalize("http://h/" + strings.Repeat("a", 64))
	_, e2 := n.Normalize("http://h/a/b/c")
	_, e3 := n.Normalize("http://h/?a&b&c")
	return errors.Is(e1, canon.ErrTooLong) &&
		errors.Is(e2, canon.ErrTooManySegments) &&
		errors.Is(e3, canon.ErrTooManyParams)
}

func inspectStable(n *canon.Normalizer) bool {
	r1, err1 := n.Inspect("HTTP://Example.com.:80/a/./b?b=2&a=1#f")
	r2, err2 := n.Inspect("HTTP://Example.com.:80/a/./b?b=2&a=1#f")
	return err1 == nil && err2 == nil &&
		r1.Canonical == r2.Canonical && r1.Kinds == r2.Kinds && r1.Rewritten
}

func concurrent(n *canon.Normalizer) bool {
	urls := make([]string, 32)
	want := make([]string, 32)
	for i := range urls {
		urls[i] = fmt.Sprintf("HTTP://h%d.com.:080/a/./b%%41?x=%d&y=%%2f", i, i)
		want[i], _ = n.Normalize(urls[i])
	}
	var wg sync.WaitGroup
	bad := make(chan bool, 1)
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for it := 0; it < 100; it++ {
				i := (g + it) % len(urls)
				got, err := n.Normalize(urls[i])
				if err != nil || got != want[i] {
					select {
					case bad <- true:
					default:
					}
				}
			}
		}(g)
	}
	wg.Wait()
	select {
	case <-bad:
		return false
	default:
		return true
	}
}
