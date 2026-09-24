package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/key"
	"ontology/list"
	"ontology/mset"
	"ontology/rank"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, name)
}

func build(keys []key.K) *mset.Mset {
	m, err := mset.New(mset.Limits{MaxLevel: key.MaxLevel, MaxElems: 1 << 20})
	if err != nil {
		panic(err)
	}
	for _, k := range keys {
		if err := m.Insert(k); err != nil {
			panic(err)
		}
	}
	return m
}

func main() {
	elems := []key.K{3, 1, 4, 1, 5, 9, 2, 6, 5, 3, 5}
	rev := make([]key.K, len(elems))
	sorted := []key.K{1, 1, 2, 3, 3, 4, 5, 5, 5, 6, 9}
	for i, k := range elems {
		rev[len(elems)-1-i] = k
	}
	a, b, c := build(elems), build(rev), build(sorted)
	check("order-independent structure", a.Equal(b) && a.Equal(c))
	check("span self-check", a.SelfCheck() == nil && b.SelfCheck() == nil)

	inv := true
	for i := 0; i < a.Size(); i++ {
		k, err := a.At(i)
		inv = inv && err == nil && a.RankOf(k) <= i
		v, err := a.At(a.RankOf(k))
		inv = inv && err == nil && v == k
	}
	check("At/RankOf inverse", inv)

	dup := a.RankOf(5) == 6 && a.Count(5) == 3
	dup = dup && a.Delete(5) == nil && a.Count(5) == 2 && a.RankOf(5) == 6 && a.Size() == 10
	check("duplicate RankOf/Count/Delete", dup)
	check("spans exact after delete", a.SelfCheck() == nil)

	n, _ := a.Range(3, 6)
	check("Range == RankOf diff", n == a.RankOf(7)-a.RankOf(3) && n == 6)

	it := a.Iterate(0)
	var got []key.K
	for it.Next() {
		got = append(got, it.Key())
		_ = a.Delete(it.Key()) // 迭代期间删除当前元素
	}
	check("iterator snapshot under writes", len(got) == 10 && a.Size() == 0 && got[6] == 5)

	m := build(elems)
	_, e1 := m.At(-1)
	_, e2 := m.At(m.Size())
	_, e3 := m.Range(9, 2)
	e4 := m.Delete(42)
	lim, _ := mset.New(mset.Limits{MaxLevel: key.MaxLevel, MaxElems: 1})
	_ = lim.Insert(0)
	e5 := lim.Insert(1)
	hk := key.K(0)
	for key.Level(hk) <= 1 {
		hk++
	}
	lim2, _ := mset.New(mset.Limits{MaxLevel: 1, MaxElems: 10})
	e6 := lim2.Insert(hk)
	check("4 decidable errors", errors.Is(e1, list.ErrOutOfRange) && errors.Is(e2, list.ErrOutOfRange) &&
		errors.Is(e3, rank.ErrBadRange) && errors.Is(e4, list.ErrNotFound) &&
		errors.Is(e5, list.ErrTooManyElements) && errors.Is(e6, list.ErrLevelLimit))
	check("usable after rejection", lim.Delete(0) == nil && lim.Insert(2) == nil && m.Insert(7) == nil && m.SelfCheck() == nil)

	const G = 8
	results := make([][]uint64, G)
	var wg sync.WaitGroup
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < m.Size(); i++ {
				k, _ := m.At(i)
				results[g] = append(results[g], uint64(k), uint64(m.RankOf(k)), uint64(m.Count(k)))
			}
			n, _ := m.Range(2, 6)
			results[g] = append(results[g], uint64(n))
		}(g)
	}
	wg.Wait()
	same := true
	for g := 1; g < G; g++ {
		if len(results[g]) != len(results[0]) {
			same = false
			break
		}
		for i := range results[g] {
			same = same && results[g][i] == results[0][i]
		}
	}
	check("concurrent reads identical", same)

	var hops [2]int
	for i, n := range []int{1000, 100000} {
		l, _ := list.New(key.MaxLevel, n)
		for k := 0; k < n; k++ {
			_ = l.Insert(key.K(k))
		}
		_, h1, _ := l.At(n / 2)
		_, h2 := l.Bound(key.K(n/2), false)
		if h1 > hops[i] {
			hops[i] = h1
		}
		if h2 > hops[i] {
			hops[i] = h2
		}
	}
	check(fmt.Sprintf("visited nodes n=1000:%d n=100000:%d (cap 80)", hops[0], hops[1]),
		hops[0] <= 80 && hops[1] <= 80)

	if failed {
		fmt.Println("FAIL demo")
		os.Exit(1)
	}
	fmt.Println("OK demo")
}
