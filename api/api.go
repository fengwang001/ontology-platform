// Package api is the outward-facing facade over a replica cluster.
package api

import (
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"slices"

	"ontology/cluster"
	"ontology/orset"
)

type API struct{ c *cluster.Cluster }

func New(n, maxTags int) (*API, error) {
	if c, err := cluster.New(n, maxTags); err != nil {
		return nil, err
	} else {
		return &API{c}, nil
	}
}

func (a *API) Add(r int, e string) error                      { return a.c.Add(r, e) }
func (a *API) Remove(r int, e string) error                   { return a.c.Remove(r, e) }
func (a *API) Merge(dst, src int) error                       { return a.c.Merge(dst, src) }
func (a *API) SyncAll() error                                 { return a.c.SyncAll() }
func (a *API) Contains(r int, e string) (bool, error)         { return a.c.Contains(r, e) }
func (a *API) Elements(r int) (map[string][]orset.Tag, error) { return a.c.Elements(r) }

// SelfCheck verifies the four invariants; one error per invariant, nil = holds.
func (a *API) SelfCheck() []error {
	return []error{checkNaive(296), checkMergeLaws(), checkAddWins(), checkNoSideEffect()}
}

func checkNaive(seed int64) error {
	const n = 4
	rng := rand.New(rand.NewSource(seed))
	a, _ := New(n, 10000)
	added, observed := map[orset.Tag]string{}, map[orset.Tag]bool{}
	var seq [n]int
	for i := 0; i < 300; i++ {
		r, e := rng.Intn(n), string(rune('a'+rng.Intn(6)))
		switch rng.Intn(4) {
		case 0:
			if a.Add(r, e) == nil {
				seq[r]++
				added[orset.Tag{R: r, S: seq[r]}] = e
			}
		case 1:
			el, _ := a.Elements(r)
			if err := a.Remove(r, e); (err == nil) != (len(el[e]) > 0) {
				return fmt.Errorf("remove mismatch: err=%v", err)
			}
			for _, t := range el[e] {
				observed[t] = true
			}
		default:
			_ = a.Merge(r, rng.Intn(n)) // capacity 10000 never reached
		}
	}
	_ = a.SyncAll()
	want := map[string]bool{}
	for t, e := range added {
		if !observed[t] {
			want[e] = true
		}
	}
	wantKeys := slices.Sorted(maps.Keys(want))
	for r := 0; r < n; r++ {
		el, _ := a.Elements(r)
		if got := slices.Sorted(maps.Keys(el)); !slices.Equal(got, wantKeys) {
			return fmt.Errorf("replica %d: %v, want %v", r, got, wantKeys)
		}
	}
	return nil
}

func checkMergeLaws() error {
	mk := func(adds ...string) *orset.Set {
		s := orset.New(0, 100)
		for _, e := range adds {
			_ = s.Add(e)
		}
		return s
	}
	y := func() *orset.Set { s := mk("b", "c"); _ = s.Remove("b"); return s }
	eq := func(a, b *orset.Set) bool { return a.Snapshot().Equal(b.Snapshot()) }
	merge := func(s *orset.Set, o ...*orset.Set) *orset.Set {
		for _, x := range o {
			_ = s.MergeFrom(x)
		}
		return s
	}
	if !eq(merge(mk("a", "b"), y()), merge(y(), mk("a", "b"))) {
		return errors.New("not commutative")
	}
	if !eq(merge(mk("a", "b"), y(), mk("c")), merge(mk("a", "b"), merge(y(), mk("c")))) {
		return errors.New("not associative")
	}
	s := mk("a", "b")
	if !eq(merge(s, s), s) {
		return errors.New("x+x != x")
	}
	s2 := merge(mk("a", "b"), y())
	if !eq(merge(s2, y()), s2) {
		return errors.New("(x+y)+y != x+y")
	}
	return nil
}

func checkAddWins() error {
	a, b := orset.New(0, 10), orset.New(1, 10)
	_ = a.Add("e")
	_ = b.MergeFrom(a)
	_ = b.Remove("e")
	_ = a.Add("e") // concurrent with the Remove, never observed by it
	_ = a.MergeFrom(b)
	_ = b.MergeFrom(a)
	if !a.Contains("e") || !b.Contains("e") {
		return errors.New("unobserved add lost")
	}
	return nil
}

func checkNoSideEffect() error {
	s := orset.New(0, 2)
	_ = s.Add("e")
	if s.Add("") == nil || s.Remove("zz") == nil {
		return errors.New("invalid op accepted")
	}
	if err := s.Add("x"); err != nil {
		return err
	}
	if ts := s.Snapshot().Adds["x"]; len(ts) != 1 || ts[0].S != 2 {
		return errors.New("tag counter skipped by rejected op")
	}
	before := s.Snapshot()
	if s.Add("f") == nil {
		return errors.New("overflow add accepted")
	}
	if !before.Equal(s.Snapshot()) {
		return errors.New("rejected op changed state")
	}
	return nil
}
