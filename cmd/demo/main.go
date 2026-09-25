// Command demo verifies the partitioned hash join with partition spill.
package main

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	"ontology/api"
	"ontology/hjoin"
	"ontology/part"
)

var (
	b0    = []part.Key{1, 5, 2, 6, 9, 3}
	p0    = []part.Key{5, 9, 3, 2, 1, 7}
	rows0 = [][]part.Key{{}, {1, 5, 9}, {2, 6}, {3}}
)

func main() {
	tag := map[bool]string{true: "OK", false: "FAIL"}
	d := []string{}
	for q, t := range rows0 {
		d = append(d, fmt.Sprintf("p%d=%v(spill=%v)", q, t, len(t) > 2))
	}
	fmt.Printf("%s partition: %s\n", tag[part.Spilled(3, 2)], strings.Join(d, " "))
	j := hjoin.New(4, 2)
	j.Build(b0)
	st := []string{}
	for _, k := range p0 {
		if ps := j.Probe([]part.Key{k}); len(ps) == 0 {
			st = append(st, fmt.Sprintf("%d:无", k))
		} else {
			st = append(st, fmt.Sprintf("%d:(%d,%d)", k, ps[0].P, ps[0].B))
		}
	}
	g := ms(j.Probe(p0))
	fmt.Printf("%s six probes (%d pairs): %s\n", tag[len(g) == 5], len(g), strings.Join(st, " "))
	wa, wb, wc := wrong(0), wrong(1), wrong(2)
	fmt.Printf("%s wrong: 甲=%d 乙=%d(漏9) 丙=%d(漏5,9,1)\n",
		tag[wa == 0 && wb == 4 && wc == 2], wa, wb, wc)
	nOK := true
	for _, s := range []int{1, 7, 50, 200} {
		bb, pp := rnd(s)
		jj := hjoin.New(7, 3)
		jj.Build(bb)
		nOK = nOK && reflect.DeepEqual(ms(jj.Probe(pp)), naive(bb, pp))
	}
	fmt.Printf("%s same as naive nested loop\n", tag[nOK])
	ns := hjoin.New(4, 1_000_000)
	ns.Build(b0)
	fmt.Printf("%s spill == no-spill\n", tag[reflect.DeepEqual(g, ms(ns.Probe(p0)))])
	_, eN := api.New(1, 1)
	_, eM := api.New(2, 0)
	en, _ := api.New(4, 2)
	_ = en.Build(b0)
	eB := en.Build([]api.Key{-1})
	_, eP := en.Probe([]api.Key{-1})
	after, _ := en.Probe(p0)
	sc := en.SelfCheck()
	distinct := errors.Is(eN, api.ErrInvalidN) && errors.Is(eM, api.ErrInvalidM) &&
		errors.Is(eB, api.ErrNegativeKey) && errors.Is(eP, api.ErrNegativeKey) &&
		eN != eM && eM != eB && len(after) == 5 && sc == nil
	fmt.Printf("%s 3 distinct errors; state unchanged after reject; SelfCheck=%v\n", tag[distinct], sc)
	mOK := true
	for _, n := range []int{100, 1000, 10000} {
		ks := make([]part.Key, n)
		for i := range ks {
			ks[i] = part.Key(i)
		}
		big := hjoin.New(n, n)
		big.Build(ks)
		mOK = mOK && len(big.Probe([]part.Key{0})) == 1
	}
	fmt.Printf("%s direct h-location m=100..10000 (counter: white-box test)\n", tag[mOK])
	cOK, varWg, varMu := true, sync.WaitGroup{}, sync.Mutex{}
	for r := 0; r < 16; r++ {
		varWg.Add(1)
		go func() {
			defer varWg.Done()
			if !reflect.DeepEqual(ms(j.Probe(p0)), g) {
				varMu.Lock()
				cOK = false
				varMu.Unlock()
			}
		}()
	}
	varWg.Wait()
	fmt.Printf("%s concurrent read-only probes identical\n", tag[cOK])
}

func ms(ps []hjoin.Pair) []int {
	o := make([]int, len(ps))
	for i, q := range ps {
		o[i] = int(q.P)
	}
	sort.Ints(o)
	return o
}

func freq(ks []part.Key) map[part.Key]int {
	m := map[part.Key]int{}
	for _, k := range ks {
		m[k]++
	}
	return m
}

func naive(b, p []part.Key) []int {
	f, o := freq(b), []int{}
	for _, k := range p {
		for c := f[k]; c > 0; c-- {
			o = append(o, int(k))
		}
	}
	sort.Ints(o)
	return o
}

func rnd(s int) (b, p []part.Key) {
	for i := 0; i < s*3+1; i++ {
		b = append(b, part.Key((i*7+s)%23))
	}
	for i := 0; i < s*2+1; i++ {
		p = append(p, part.Key((i*11+s)%29))
	}
	return b, p
}

func wrong(mode int) int {
	c := 0
	for _, pk := range p0 {
		q := part.H(pk, 4)
		if mode == 0 {
			q = (q + 1) % 4
		}
		row := rows0[q]
		if mode == 1 && len(row) > 2 {
			row = row[:2]
		}
		if mode == 2 && len(row) > 2 {
			continue
		}
		c += freq(row)[pk]
	}
	return c
}
