// Package api is the public face of the sticky-assign consumer group.
package api

import (
	"fmt"
	"maps"
	"slices"

	"ontology/group"
)

type Change = group.Change // one membership change inside a batch

func Joining(id string) Change { return group.Joining(id) }
func Leaving(id string) Change { return group.Leaving(id) }

type API struct{ g *group.Group } // one consumer group, sticky assignment

func New(n, maxMembers int) *API { return &API{g: group.New(n, maxMembers)} }

func (a *API) Rebalance(changes ...Change) (int, error) { return a.g.Apply(changes) }

func (a *API) Assignment() map[string][]int { return a.g.Assignment() }

var selfBatches = [][]Change{{Joining("b")}, {Joining("a")}, {Joining("c")}, {Joining("d")}, {Leaving("a")}, {Joining("e")}, {Leaving("c")}}

func (a *API) SelfCheck() error {
	if err := checkSequence(7, selfBatches); err != nil {
		return err
	}
	b := New(4, 2)
	b.Rebalance(Joining("x"), Joining("y"))
	before, gen := fmt.Sprint(b.Assignment()), b.g.Generation()
	for _, bad := range [][]Change{{Joining("")}, {Joining("x")}, {Leaving("ghost")}, {Joining("z"), Joining("w")}, {Joining("z"), Leaving("z")}} {
		if _, err := b.Rebalance(bad...); err == nil {
			return fmt.Errorf("selfcheck: invalid batch accepted")
		}
	}
	if fmt.Sprint(b.Assignment()) != before || b.g.Generation() != gen {
		return fmt.Errorf("selfcheck: rejection mutated state")
	}
	return nil
}

func checkSequence(n int, batches [][]Change) error {
	run := func(rev bool) (snaps []map[string][]int, migs []int) {
		a := New(n, 8)
		for _, batch := range batches {
			b := slices.Clone(batch)
			if rev {
				slices.Reverse(b)
			}
			m, err := a.Rebalance(b...)
			if err != nil {
				panic(err) // built-in sequences are valid
			}
			snaps, migs = append(snaps, a.Assignment()), append(migs, m)
		}
		return snaps, migs
	}
	s1, m1 := run(false)
	s2, m2 := run(true)
	if fmt.Sprint(s1, m1) != fmt.Sprint(s2, m2) {
		return fmt.Errorf("selfcheck: non-deterministic")
	}
	prev := map[int]string{}
	for i, snap := range s1 {
		if err := checkBalanced(n, snap); err != nil {
			return err
		}
		if w := bruteMin(n, slices.Sorted(maps.Keys(snap)), prev); m1[i] != w {
			return fmt.Errorf("selfcheck: batch %d mig %d != min %d", i, m1[i], w)
		}
		prev = map[int]string{}
		for id, ps := range snap {
			for _, p := range ps {
				prev[p] = id
			}
		}
	}
	return nil
}

func checkBalanced(n int, snap map[string][]int) error {
	own, lo, hi := make([]int, n), n, 0
	for _, ps := range snap {
		lo, hi = min(lo, len(ps)), max(hi, len(ps))
		for _, p := range ps {
			if uint(p) >= uint(n) || own[p] > 0 {
				return fmt.Errorf("selfcheck: partition %d bad", p)
			}
			own[p]++
		}
	}
	if len(snap) == 0 {
		return nil
	}
	for _, c := range own {
		if c != 1 {
			return fmt.Errorf("selfcheck: incomplete")
		}
	}
	if hi-lo > 1 {
		return fmt.Errorf("selfcheck: unbalanced")
	}
	return nil
}

// bruteMin returns the minimal migration count over all balanced assignments.
func bruteMin(n int, members []string, prev map[int]string) int {
	k := len(members)
	if k == 0 || n == 0 {
		return 0
	}
	slices.Sort(members)
	base, extra, best, cnt := n/k, n%k, n+1, make([]int, k)
	var rec func(p, mig int)
	rec = func(p, mig int) {
		if mig >= best {
			return
		}
		if p == n {
			plus := 0
			for _, c := range cnt {
				if c < base {
					return
				}
				plus += c - base
			}
			if plus == extra {
				best = mig
			}
			return
		}
		for i, m := range members {
			if cnt[i] > base {
				continue
			}
			cnt[i]++
			d := 0
			if prev[p] != "" && prev[p] != m {
				d = 1
			}
			rec(p+1, mig+d)
			cnt[i]--
		}
	}
	rec(0, 0)
	return best
}
