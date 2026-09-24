package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"slices"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/idx"
)

var failed bool

func check(name string, ok bool) {
	failed = failed || !ok
	fmt.Println(map[bool]string{true: "OK", false: "FAIL"}[ok], name)
}

func main() {
	l, _ := api.New(1000, 100)
	steps := []struct {
		off, size, pos int64
		newEntry       bool
	}{{1000, 60, 0, false}, {1001, 50, 60, false}, {1003, 40, 110, true}, {1004, 60, 150, false}, {1007, 30, 210, true}, {1008, 80, 240, false}, {1010, 20, 320, true}, {1012, 50, 340, false}}
	okAll := true
	for _, s := range steps {
		before := len(l.Entries())
		p, err := l.Append(s.off, s.size)
		okAll = okAll && err == nil && p == s.pos && (len(l.Entries()) > before) == s.newEntry
	}
	check("append8 pos+entry", okAll)
	check("full index", slices.Equal(l.Entries(),
		[]idx.Entry{{Rel: 3, Pos: 110}, {Rel: 7, Pos: 210}, {Rel: 10, Pos: 320}}))
	o1, p1, n1, e1 := l.Lookup(1004)
	o2, p2, n2, e2 := l.Lookup(1006)
	check("lookup 1004/1006", e1 == nil && o1 == 1004 && p1 == 150 && n1 == 2 &&
		e2 == nil && o2 == 1007 && p2 == 210 && n2 == 3)
	check(">= boundary", slices.Contains(l.Entries(), idx.Entry{Rel: 7, Pos: 210}))
	es := l.Entries()
	_, err := l.Append(1000+2147483648, 10)
	pNext, _ := l.Append(1013, 40)
	check("overflow rejected, state intact", errors.Is(err, api.ErrRelOverflow) &&
		slices.Equal(es, l.Entries()) && pNext == 390)
	check("four distinct errors", fourErrors())
	check("naive match random", naiveMatch())
	check("large-m bounded", largeM())
	check("concurrent lookups", concurrent())
	check("selfcheck", func() bool { s, _ := api.New(0, 1); return s.SelfCheck() == nil }())
	if failed {
		os.Exit(1)
	}
}

func fourErrors() bool {
	l, _ := api.New(100, 10)
	l.Append(100, 5)
	_, e1 := l.Append(100, 5)       // 参数或追加非法
	_, e2 := l.Append(100+1<<31, 5) // 相对位点溢出
	_, _, _, e3 := l.Lookup(99)     // 查找低于基位点
	_, _, _, e4 := l.Lookup(101)    // 未找到
	sents := []error{api.ErrInvalid, api.ErrRelOverflow, api.ErrBelowBase, api.ErrNotFound}
	for i, e := range []error{e1, e2, e3, e4} {
		for j, s := range sents {
			if errors.Is(e, s) != (i == j) {
				return false
			}
		}
	}
	return true
}

func naiveMatch() bool {
	rng := rand.New(rand.NewSource(7))
	l, _ := api.New(0, 16)
	var offs, poss []int64
	for i := 0; i < 300; i++ {
		off := int64(rng.Intn(3))
		if i > 0 {
			off = offs[i-1] + 1 + int64(rng.Intn(3))
		}
		p, _ := l.Append(off, int64(rng.Intn(9)+1))
		offs, poss = append(offs, off), append(poss, p)
	}
	for t := int64(0); t <= offs[len(offs)-1]+1; t++ {
		o, p, _, err := l.Lookup(t)
		no, np, nerr := int64(0), int64(0), api.ErrNotFound
		if i := slices.IndexFunc(offs, func(o int64) bool { return o >= t }); i >= 0 {
			no, np, nerr = offs[i], poss[i], nil
		}
		if (err == nil) != (nerr == nil) || (err == nil && (o != no || p != np)) {
			return false
		}
	}
	return true
}

func largeM() bool {
	for _, m := range []int{100, 1000, 10000} {
		l, _ := api.New(0, 40)
		for i := 0; i < m; i++ {
			l.Append(int64(i), 10)
		}
		for j := 0; j < 50; j++ {
			if o, _, _, err := l.Lookup(int64(j * m / 50)); err != nil || o != int64(j*m/50) {
				return false
			}
		}
	}
	return true
}

func concurrent() bool {
	const n = 1000
	l, _ := api.New(0, 25)
	poss := make([]int64, n)
	var count atomic.Int64
	var bad atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			p, _ := l.Append(int64(i), 10)
			poss[i] = p
			count.Store(int64(i + 1))
		}
	}()
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for j := 0; j < 2000; j++ {
				if c := count.Load(); c > 0 {
					i := rng.Int63n(c)
					if o, p, _, err := l.Lookup(i); err != nil || o != i || p != poss[i] {
						bad.Store(true)
					}
				}
			}
		}(int64(r))
	}
	wg.Wait()
	return !bad.Load()
}
