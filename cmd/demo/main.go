package main

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"ontology/api"
	"ontology/orset"
	"os"
	"sync"
)

func check(name string, ok bool) bool {
	fmt.Println(map[bool]string{true: "OK", false: "FAIL"}[ok], name)
	return ok
}
func es(s *api.Set, r int) string                 { m, _ := s.Elements(r); return fmt.Sprint(m) }
func ad(s *api.Set, r int, e string) func() error { return func() error { return s.Add(r, e) } }
func rm(s *api.Set, r int, e string) func() error { return func() error { return s.Remove(r, e) } }
func mg(s *api.Set, d, src int) func() error      { return func() error { return s.Merge(d, src) } }

func trace() bool {
	s, _ := api.New(2, 1000)
	steps := []func() error{
		ad(s, 0, "x"), mg(s, 1, 0), rm(s, 1, "x"), ad(s, 0, "x"), ad(s, 0, "y"), ad(s, 1, "y"), rm(s, 0, "y"), mg(s, 0, 1), mg(s, 1, 0), rm(s, 1, "x"), mg(s, 0, 1),
	}
	want := [][2]string{
		{"map[x:[{0 1}]]", "map[]"}, {"map[x:[{0 1}]]", "map[x:[{0 1}]]"}, {"map[x:[{0 1}]]", "map[]"}, {"map[x:[{0 1} {0 2}]]", "map[]"}, {"map[x:[{0 1} {0 2}] y:[{0 3}]]", "map[]"}, {"map[x:[{0 1} {0 2}] y:[{0 3}]]", "map[y:[{1 1}]]"},
		{"map[x:[{0 1} {0 2}]]", "map[y:[{1 1}]]"}, {"map[x:[{0 2}] y:[{1 1}]]", "map[y:[{1 1}]]"}, {"map[x:[{0 2}] y:[{1 1}]]", "map[x:[{0 2}] y:[{1 1}]]"}, {"map[x:[{0 2}] y:[{1 1}]]", "map[y:[{1 1}]]"}, {"map[y:[{1 1}]]", "map[y:[{1 1}]]"},
	}
	for i, st := range steps {
		if st() != nil || es(s, 0) != want[i][0] || es(s, 1) != want[i][1] {
			return false
		}
	}
	return true
}

func reordered() bool {
	s, _ := api.New(2, 1000)
	ok := s.Add(0, "x") == nil && s.Add(0, "x") == nil && s.Merge(1, 0) == nil && s.Remove(1, "x") == nil &&
		s.Add(0, "y") == nil && s.Add(1, "y") == nil && s.Remove(0, "y") == nil && s.Merge(0, 1) == nil && s.Merge(1, 0) == nil
	return ok && es(s, 0) == "map[y:[{1 1}]]" && errors.Is(s.Remove(1, "x"), orset.ErrNotFound) && es(s, 1) == "map[y:[{1 1}]]"
}
func allMatch(s *api.Set, n int, live map[string]bool) bool {
	ok := s.SyncAll() == nil
	for r := 0; r < n; r++ {
		m, _ := s.Elements(r)
		ok = ok && len(m) == len(live)
		for e := range m {
			ok = ok && live[e]
		}
	}
	return ok
}
func randomVsNaive() bool {
	s, _ := api.New(3, 100000)
	live, added := map[string]bool{}, []string{}
	for range 400 {
		if v := rand.IntN(3); v == 0 {
			e := fmt.Sprintf("e%d", len(added))
			added = append(added, e)
			live[e] = true
			s.Add(rand.IntN(3), e)
		} else if v == 1 && len(added) > 0 {
			e := added[rand.IntN(len(added))]
			if s.Remove(rand.IntN(3), e) == nil {
				delete(live, e)
			}
		} else if v == 2 {
			s.Merge(rand.IntN(3), rand.IntN(3))
		}
	}
	return allMatch(s, 3, live)
}
func mergeLaws() bool {
	build := func() *api.Set {
		s, _ := api.New(3, 1000)
		for i, e := range []string{"a", "b", "b", "c", "d"} {
			s.Add(i/2, e)
		}
		return s
	}
	p, q := build(), build()
	p.Merge(0, 1)
	p.Merge(0, 1)
	p.Merge(0, 2)
	q.Merge(1, 0)
	q.Merge(1, 2)
	q.Merge(0, 1)
	return es(p, 0) == es(q, 0) && es(p, 0) == es(q, 1)
}
func failures() (bool, bool) {
	_, e0 := api.New(0, 1)
	s, _ := api.New(2, 1)
	s.Add(0, "a")
	pairs := [][2]error{
		{e0, orset.ErrInvalidArgument}, {s.Add(5, "b"), orset.ErrInvalidArgument}, {s.Merge(0, 7), orset.ErrInvalidArgument}, {s.Add(0, ""), orset.ErrEmptyElement}, {s.Remove(0, "zz"), orset.ErrNotFound}, {s.Add(0, "b"), orset.ErrCapacity},
	}
	seen := map[error]bool{}
	for _, p := range pairs {
		if seen[p[1]] = true; !errors.Is(p[0], p[1]) {
			return false, false
		}
	}
	t, _ := api.New(2, 4)
	for _, op := range []func() error{
		ad(t, 0, "a"), ad(t, 0, ""), rm(t, 0, "zz"), ad(t, 7, "b"), mg(t, 0, 9), ad(t, 0, "b"),
	} {
		op()
	}
	return len(seen) == 4, es(t, 0) == "map[a:[{0 1}] b:[{0 2}]]"
}
func concurrent() bool {
	s, _ := api.New(4, 100000)
	var wg sync.WaitGroup
	live := map[string]bool{}
	for g := 0; g < 4; g++ {
		for j := 1; j < 20; j += 2 {
			live[fmt.Sprintf("g%d-%d", g, j)] = true
		}
		wg.Go(func() {
			for j := 0; j < 20; j++ {
				s.Add(g, fmt.Sprintf("g%d-%d", g, j))
				s.Remove(g, fmt.Sprintf("g%d-%d", g, j&^1)) // even ids die; odd j re-removes j-1, a harmless no-op
			}
		})
	}
	wg.Go(func() {
		for range 400 {
			s.Merge(rand.IntN(4), rand.IntN(4))
		}
	})
	wg.Wait()
	return allMatch(s, 4, live)
}

func main() {
	d, n := failures()
	ok := check("11-step trace + live tags @8/@11", trace())
	ok = check("reordered: step8 set + step10 err", reordered()) && ok
	ok = check("random ops == naive reference", randomVsNaive()) && ok
	ok = check("merge comm/assoc/idempotent", mergeLaws()) && ok
	ok = check("four distinct decidable errors", d) && ok
	ok = check("rejected ops leave no trace", n) && ok
	ok = check("lookup cost independent of m", orset.VerifyLookupCost(100, 1000, 10000)) && ok
	ok = check("concurrent add/remove/merge", concurrent()) && ok
	os.Exit(map[bool]int{true: 1, false: 0}[!ok])
}
