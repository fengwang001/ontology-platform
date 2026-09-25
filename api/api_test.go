package api_test

import (
	"errors"
	"fmt"
	"ontology/api"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func k(i int) string { return "k" + strconv.Itoa(i) }
func chk(t *testing.T, c bool, f string, a ...any) {
	if !c {
		t.Errorf(f, a...)
	}
}
func must(t *testing.T, e error) { chk(t, e == nil, "unexpected error: %v", e) }
func sig(s *api.Store) string {
	var b strings.Builder
	for _, x := range append(s.HotKeys(), s.ColdKeys()...) {
		v, _ := s.Value(x)
		fmt.Fprintf(&b, "%s=%d,", x, v)
	}
	return b.String()
}
func parDo(n int, f func(i int)) {
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); f(i) }(i)
	}
	wg.Wait()
}
func runSteps(t *testing.T, s *api.Store, st []string) {
	for i, q := range st {
		p := strings.Split(q, "|")
		f := strings.Fields(p[0])
		n, _ := strconv.Atoi(f[2])
		if f[0] == "P" {
			chk(t, s.Put(f[1], int64(n)) == nil, "step %d put: %s", i+1, f[1])
		} else {
			v, e := s.Get(f[1])
			chk(t, e == nil && v == int64(n), "step %d Get=%d %v want %d", i+1, v, e, n)
		}
		h, c := strings.Join(s.HotKeys(), ""), strings.Join(s.ColdKeys(), "")
		chk(t, h == strings.TrimSpace(p[1]) && c == strings.TrimSpace(p[2]),
			"step %d h=%q c=%q want %q %q", i+1, h, c, p[1], p[2])
	}
}
func TestNaiveReference(t *testing.T)           { drive(t) }
func TestTiersDisjointAndCapacity(t *testing.T) { drive(t) }
func drive(t *testing.T) {
	cases := []struct{ hc, mc, ks, n int }{{1, 3, 3, 60}, {2, 4, 4, 80}, {3, 7, 7, 120}, {16, 11, 11, 300}}
	for _, c := range cases {
		s, _ := api.New(c.hc, c.mc)
		nv := map[string]int64{}
		for i := 0; i < c.n; i++ {
			key := k((i*7 + 3) % c.ks)
			if i%2 == 0 {
				chk(t, s.Put(key, int64(i+1)) == nil, "unexpected put failure")
				nv[key] = int64(i + 1)
			} else {
				s.Get(key) // ErrNotFound on a fresh key changes nothing
			}
			hot, cold := s.HotKeys(), s.ColdKeys()
			chk(t, len(hot) <= c.hc && len(cold) <= c.mc && len(hot)+len(cold) == len(nv),
				"cap/union h=%d c=%d ref=%d", len(hot), len(cold), len(nv))
			seen := map[string]bool{}
			for _, x := range append(hot, cold...) {
				chk(t, !seen[x], "%s in both tiers", x)
				seen[x] = true
				v, _ := s.Value(x)
				chk(t, v == nv[x], "%s=%d want %d", x, v, nv[x])
			}
			chk(t, sort.StringsAreSorted(cold), "cold unsorted %v", cold)
		}
	}
}
func TestEightStepTrace(t *testing.T) {
	trace := []string{"P A 1 | A | ", "P B 2 | BA | ", "G A 1 | AB | ", "P C 3 | CA | B", "G B 2 | BC | A", "P D 4 | DB | AC", "G A 1 | AD | BC", "P E 5 | EA | BCD"}
	s, _ := api.New(2, 100)
	runSteps(t, s, trace)
	s.Value("A")
	s.Value("B")
	h, c := strings.Join(s.HotKeys(), ""), strings.Join(s.ColdKeys(), "")
	chk(t, h == "EA" && c == "BCD", "Value moved tiers %q %q", h, c)
}
func TestHotKeysFollowAtime(t *testing.T) {
	cases := []struct {
		hc int
		st []string
	}{
		{4, []string{"P A 1 | A | ", "P B 2 | BA | ", "G A 1 | AB | ", "P C 3 | CAB | ", "P A 9 | ACB | "}},
		{2, []string{"P A 1 | A | ", "P B 2 | BA | ", "P C 3 | CB | A", "G A 1 | AC | B", "P B 20 | BA | C"}},
	}
	for _, tc := range cases {
		s, _ := api.New(tc.hc, 10)
		runSteps(t, s, tc.st)
	}
}
func TestRejectedOperationsAreAtomic(t *testing.T) {
	for _, c := range [][2]int{{0, 1}, {-1, 1}, {1, 0}, {1, -1}, {0, 0}} {
		_, e := api.New(c[0], c[1])
		chk(t, errors.Is(e, api.ErrBadCap), "New%v=%v", c, e)
	}
	s, _ := api.New(1, 1)
	notrace := func(want error, f func() error) {
		b := sig(s)
		e := f()
		chk(t, errors.Is(e, want) && sig(s) == b, "want %v e=%v %q->%q", want, e, b, sig(s))
	}
	notrace(api.ErrEmptyKey, func() error { return s.Put("", 1) })
	notrace(api.ErrNotFound, func() error { _, e := s.Get("z"); return e })
	must(t, s.Put("a", 1))
	must(t, s.Put("b", 2))
	notrace(api.ErrColdFull, func() error { return s.Put("c", 3) })
	va, _ := s.Get("a")
	chk(t, va == 1, "Get(a)=%d want 1", va)
	must(t, s.Put("b", 22))
	vb, _ := s.Value("b")
	chk(t, vb == 22, "b=%d want 22", vb)
	va2, _ := s.Value("a")
	chk(t, va2 == 1, "a value lost across tier moves")
	must(t, s.SelfCheck()) // SelfCheck also asserts the four errors are distinct
}
func TestConcurrentGetsOnFullHotTier(t *testing.T) {
	const n = 200
	s, _ := api.New(n, n*2)
	for i := 0; i < n; i++ {
		must(t, s.Put(k(i), int64(i)))
	}
	parDo(n, func(i int) {
		v, e := s.Get(k(i))
		chk(t, e == nil && v == int64(i), "%s=%d %v", k(i), v, e)
	})
	chk(t, len(s.HotKeys()) <= n, "hot=%d > %d", len(s.HotKeys()), n)
	parDo(n, func(i int) {
		v, _ := s.Value(k(i))
		chk(t, v == int64(i), "%s=%d want %d", k(i), v, i)
	})
	must(t, s.SelfCheck())
}
func TestConcurrentValueSameKey(t *testing.T) {
	s2, _ := api.New(2, 2)
	must(t, s2.Put("x", 42))
	parDo(64, func(int) { v, _ := s2.Value("x"); chk(t, v == 42, "Value got %d want 42", v) })
}
