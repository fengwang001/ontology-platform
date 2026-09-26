package api_test

import (
	"errors"
	"math/rand"
	"strings"
	"sync"
	"testing"

	"ontology/api"
	"ontology/period"
)

func naivePeriodic(s string, p int) bool {
	for i := p; i < len(s); i++ {
		if s[i] != s[i-p] {
			return false
		}
	}
	return true
}

func naiveMin(s string) int {
	for p := 1; p <= len(s); p++ {
		if naivePeriodic(s, p) {
			return p
		}
	}
	return 0
}

func sampleStrings() []string {
	ss := []string{"a", "aa", "ab", "ababab", "abababa", "abcabcabc", "abcde", "aaaa", "aabaaab"}
	rng := rand.New(rand.NewSource(11))
	for i := 0; i < 100; i++ {
		b := make([]byte, 1+rng.Intn(40))
		for j := range b {
			b[j] = byte('a' + rng.Intn(3))
		}
		ss = append(ss, string(b))
	}
	return ss
}

func TestMinPeriodMatchesNaive(t *testing.T) {
	for _, s := range sampleStrings() {
		c, _ := api.New(s)
		got, _ := c.MinPeriod()
		if got != naiveMin(s) || period.MinPeriod(s) != naiveMin(s) {
			t.Fatalf("%q: MinPeriod=%d want %d", s, got, naiveMin(s))
		}
	}
}

func TestIsPeriodicMatchesNaive(t *testing.T) {
	for _, s := range sampleStrings() {
		c, _ := api.New(s)
		for p := 1; p <= len(s); p++ {
			if got, err := c.IsPeriodic(p); err != nil || got != naivePeriodic(s, p) {
				t.Fatalf("%q p=%d: got %v,%v want %v", s, p, got, err, naivePeriodic(s, p))
			}
		}
	}
}

func TestPeriodBorderDuality(t *testing.T) {
	for _, s := range sampleStrings() {
		lb := 0
		for b := len(s) - 1; b > 0; b-- {
			if s[:b] == s[len(s)-b:] {
				lb = b
				break
			}
		}
		if period.MinPeriod(s) != len(s)-lb {
			t.Fatalf("%q: MinPeriod != n - longest border", s)
		}
		for p := 1; p <= len(s); p++ {
			b := len(s) - p
			dual := p == len(s) || (b > 0 && s[:b] == s[len(s)-b:])
			if period.IsPeriodic(s, p) != dual {
				t.Fatalf("%q p=%d: period/border duality broken", s, p)
			}
		}
	}
}

func TestIsPower(t *testing.T) {
	cases := map[string]bool{
		"ababab": true, "aaaa": true, "abcabcabc": true, "aa": true,
		"abababa": false, "abcde": false, "a": false, "aabaaab": false,
	}
	for s, want := range cases {
		c, _ := api.New(s)
		if c.IsPower() != want || period.IsPower(s) != want {
			t.Fatalf("%q: IsPower want %v", s, want)
		}
	}
}

func TestRejectionLeavesState(t *testing.T) {
	c, _ := api.New("ababab")
	_, e1 := api.New("")
	_, e2 := api.New(strings.Repeat("x", 1<<20+1))
	_, e3 := c.IsPeriodic(0)
	_, e4 := c.IsPeriodic(7)
	for i, ch := range []struct{ got, want error }{{e1, api.ErrEmpty}, {e2, api.ErrTooLong}, {e3, api.ErrBadPeriod}, {e4, api.ErrBadPeriod}} {
		if !errors.Is(ch.got, ch.want) {
			t.Fatalf("case %d: got %v want %v", i, ch.got, ch.want)
		}
	}
	if errors.Is(api.ErrEmpty, api.ErrTooLong) || errors.Is(api.ErrTooLong, api.ErrBadPeriod) || errors.Is(api.ErrEmpty, api.ErrBadPeriod) {
		t.Fatal("sentinel errors must be mutually distinct")
	}
	if got, _ := c.MinPeriod(); got != 2 || !c.IsPower() {
		t.Fatal("state changed after rejections")
	}
	if ok, _ := c.IsPeriodic(2); !ok {
		t.Fatal("checker unusable after rejections")
	}
}

func TestConcurrentReadOnly(t *testing.T) {
	c, _ := api.New("abababa")
	type res struct{ mp, ip, pw, sc any }
	const G = 32
	got := make([]res, G)
	var wg sync.WaitGroup
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := 0; k < 50; k++ {
				mp, _ := c.MinPeriod()
				ip, _ := c.IsPeriodic(2)
				got[g] = res{mp, ip, c.IsPower(), c.SelfCheck() == nil}
			}
		}(g)
	}
	wg.Wait()
	want := res{2, true, false, true}
	for g := 0; g < G; g++ {
		if got[g] != want {
			t.Fatalf("goroutine %d: %+v != %+v", g, got[g], want)
		}
	}
}
