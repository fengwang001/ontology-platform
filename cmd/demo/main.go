// Command demo exercises the key-group assignment and rescale packages.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/kgrp"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Printf("%s %s\n", map[bool]string{true: "OK", false: "FAIL"}[ok], name)
}

func kgOf(k string) int { g, _ := kgrp.KeyGroup(k, 10); return g }

// genKeys finds n keys of the given format whose groups are all allowed.
func genKeys(n int, allowed map[int]bool, format string) []string {
	out := make([]string, 0, n)
	for i := 0; len(out) < n; i++ {
		if k := fmt.Sprintf(format, i); allowed[kgOf(k)] {
			out = append(out, k)
		}
	}
	return out
}

func main() {
	e, _ := api.New(10, 3)
	for i := 1; i <= 8; i++ {
		_ = e.Put(fmt.Sprintf("u%d", i), int64(i))
	}
	kgOK := true
	for i, w := range []int{6, 7, 8, 9, 0, 1, 2, 3} {
		if kgOf(fmt.Sprintf("u%d", i+1)) != w {
			kgOK = false
		}
	}
	check("u1..u8 keygroups = [6 7 8 9 0 1 2 3]", kgOK)

	r3 := e.Ranges()
	plan, err := e.Rescale(4)
	var moved []int
	for _, ip := range plan.Instances {
		for _, m := range ip.Incoming {
			moved = append(moved, m.KeyGroup)
		}
	}
	rOK := fmt.Sprint(r3) == "[[0 4] [4 7] [7 10]]" &&
		fmt.Sprint(e.Ranges()) == "[[0 3] [3 5] [5 8] [8 10]]"
	check(fmt.Sprintf("ranges p3/p4; rescale groups=%v moved=%d", moved, plan.MovedKeys),
		rOK && err == nil && fmt.Sprint(moved) == "[3 5 6 8 9]" && plan.MovedKeys == 4)

	consOK := true
	for i := 1; i <= 8; i++ {
		if v, ok := e.Get(fmt.Sprintf("u%d", i)); !ok || v != int64(i) {
			consOK = false
		}
	}
	check("values conserved after rescale", consOK)

	naiveOK := true
	for mp := 1; mp <= 64 && naiveOK; mp++ {
		for p := 1; p <= mp; p++ {
			en, _ := api.New(mp, p)
			r := en.Ranges()
			for kg := 0; kg < mp; kg++ {
				iv := r[kgrp.Owner(kg, p, mp)]
				if !(iv[0] <= kg && kg < iv[1]) {
					naiveOK = false
				}
			}
		}
	}
	check("ranges == per-group formula (maxP 1..64); SelfCheck", naiveOK && e.SelfCheck() == nil)

	_, e1 := api.New(0, 1)
	_, e2 := api.New(10, 0)
	_, e2b := api.New(10, 11)
	e3 := e.Put("", 1)
	check("three distinct sentinel errors",
		errors.Is(e1, api.ErrMaxPInvalid) && errors.Is(e2, api.ErrPInvalid) &&
			errors.Is(e2b, api.ErrPInvalid) && errors.Is(e3, api.ErrEmptyKey) && e1 != e2 && e2 != e3)

	before := e.Ranges()
	_, bad := e.Rescale(99)
	traceOK := bad != nil && fmt.Sprint(e.Ranges()) == fmt.Sprint(before)
	if v, ok := e.Get("u1"); !ok || v != 1 {
		traceOK = false
	}
	check("rejected op leaves no trace", traceOK)

	// p3->4 migratory groups are {3,5,6,8,9}; m keys go only in the rest, plus
	// 2 fixed keys in moving groups. MovedKeys stays 2 as m scales.
	stay := map[int]bool{0: true, 1: true, 2: true, 4: true, 7: true}
	subOK := true
	for _, m := range []int{100, 1000, 10000} {
		big, _ := api.New(10, 3)
		for _, k := range genKeys(m, stay, "z%d") {
			_ = big.Put(k, 1)
		}
		_ = big.Put(genKeys(1, map[int]bool{3: true}, "m%d")[0], 1)
		_ = big.Put(genKeys(1, map[int]bool{6: true}, "m%d")[0], 1)
		if pl, pe := big.Rescale(4); pe != nil || pl.MovedKeys != 2 {
			subOK = false
		}
	}
	check("visited entries bounded, m=100..10000", subOK)

	const n = 16
	var wg sync.WaitGroup
	res, ov := make([]string, n), make([]string, n)
	wg.Add(n)
	for g := 0; g < n; g++ {
		go func(g int) {
			defer wg.Done()
			res[g] = fmt.Sprint(e.Ranges())
			got := ""
			for i := 1; i <= 8; i++ {
				k := fmt.Sprintf("u%d", i)
				v, _ := e.Get(k)
				got += fmt.Sprintf("(%d,%d)", e.Owner(k), v)
			}
			ov[g] = got
		}(g)
	}
	wg.Wait()
	concOK := true
	for g := 1; g < n; g++ {
		if res[g] != res[0] || ov[g] != ov[0] {
			concOK = false
		}
	}
	check("concurrent readers field-identical", concOK)

	if failed {
		os.Exit(1)
	}
}
