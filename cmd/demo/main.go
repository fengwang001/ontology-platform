// Command demo verifies key-group assignment and rescale.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/kgrp"
	"ontology/rescale"
)

func check(name string, ok bool) {
	if !ok {
		fmt.Println("FAIL " + name)
		os.Exit(1)
	}
	fmt.Println("OK   " + name)
}
func ranges(p, maxP int) [][2]int {
	r := make([][2]int, p)
	for i := range r {
		r[i][0], r[i][1] = kgrp.RangeBounds(i, p, maxP)
	}
	return r
}

// keyForGroup scans short keys until one hashes into kg.
func keyForGroup(kg, maxP int, used map[string]bool) string {
	for j := 0; ; j++ {
		k := fmt.Sprintf("kg%d-%d", kg, j)
		if !used[k] && int(kgrp.Hash(k)%uint32(maxP)) == kg {
			used[k] = true
			return k
		}
	}
}
func main() {
	const maxP = 10
	wantKG := []int{6, 7, 8, 9, 0, 1, 2, 3}
	kgOK := true
	check("ranges p=3 [0,4)[4,7)[7,10); p=4 [0,3)[3,5)[5,8)[8,10)",
		fmt.Sprint(ranges(3, maxP)) == "[[0 4] [4 7] [7 10]]" &&
			fmt.Sprint(ranges(4, maxP)) == "[[0 3] [3 5] [5 8] [8 10]]")
	consistent := true
	for mp := 1; mp <= 64 && consistent; mp++ {
		for p := 1; p <= mp && consistent; p++ {
			r := ranges(p, mp)
			i := 0
			for kg := 0; kg < mp; kg++ {
				for kg >= r[i][1] {
					i++
				}
				if i != kgrp.Instance(kg, p, mp) {
					consistent = false
				}
			}
		}
	}
	check("ranges == per-key-group inst(), maxP 1..64 all p", consistent)
	st, _ := rescale.New(maxP, 3)
	for n := 1; n <= 8; n++ {
		k := fmt.Sprintf("u%d", n)
		if kg, e := kgrp.KeyGroup(k, maxP); e != nil || kg != wantKG[n-1] {
			kgOK = false
		}
		_ = st.Put(k, int64(n))
	}
	check("key groups u1..u8 = 6,7,8,9,0,1,2,3", kgOK)
	plan, moved, _ := st.Rescale(4)
	groups := []int{}
	for _, ip := range plan {
		for _, mv := range ip.Incoming {
			groups = append(groups, mv.KeyGroup)
		}
	}
	conserved := true
	for n := 1; n <= 8; n++ {
		v, ok := st.Get(fmt.Sprintf("u%d", n))
		conserved = conserved && ok && v == int64(n)
	}
	check("Rescale(4): groups [3 5 6 8 9], moved=4, values conserved",
		fmt.Sprint(groups) == "[3 5 6 8 9]" && moved == 4 && conserved)
	a, _ := api.New(maxP, 3)
	e1 := func() error { _, e := api.New(0, 1); return e }()
	e2 := func() error { _, e := a.Rescale(99); return e }()
	e3 := a.Put("", 1)
	before := fmt.Sprint(a.Ranges())
	check("three distinct errors; rejected ops leave no trace; SelfCheck",
		errors.Is(e1, api.ErrInvalidMaxP) && errors.Is(e2, api.ErrInvalidParallelism) &&
			errors.Is(e3, api.ErrEmptyKey) && e1 != e2 && e2 != e3 &&
			fmt.Sprint(a.Ranges()) == before && a.SelfCheck() == nil)

	bigOK := true
	for _, m := range []int{100, 1000, 10000} {
		bs, _ := rescale.New(128, 4)
		used := map[string]bool{}
		migN := 0
		for kg := 0; kg < 128; kg++ {
			if kgrp.Instance(kg, 4, 128) != kgrp.Instance(kg, 5, 128) {
				_ = bs.Put(keyForGroup(kg, 128, used), 1)
				migN++
			}
		}
		for j, put := 0, 0; put < m; j++ {
			k := fmt.Sprintf("bulk-%d", j)
			kg := int(kgrp.Hash(k) % 128)
			if kgrp.Instance(kg, 4, 128) == kgrp.Instance(kg, 5, 128) && !used[k] {
				_ = bs.Put(k, int64(j))
				put++
			}
		}
		_, mv, err := bs.Rescale(5)
		bigOK = bigOK && err == nil && mv == migN
	}
	check("visited/moved keys constant as m grows 100..10000", bigOK)
	full, _ := rescale.New(31, 3)
	keys := []string{}
	for i := 0; i < 200; i++ {
		k := fmt.Sprintf("cc-%d", i)
		keys, _ = append(keys, k), full.Put(k, int64(i))
	}
	snap := func() string {
		s := fmt.Sprint(full.Ranges())
		for _, k := range keys {
			v, _ := full.Get(k)
			s += fmt.Sprintf("%s@%d=%d;", k, full.Owner(k), v)
		}
		return s
	}
	want := snap()
	const N = 16
	got := make([]string, N)
	var wg sync.WaitGroup
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) { defer wg.Done(); got[g] = snap() }(g)
	}
	wg.Wait()
	concOK := true
	for _, s := range got {
		concOK = concOK && s == want
	}
	check("16 concurrent readers see identical ranges/owners/values", concOK)
	fmt.Println("OK all checks")
}
