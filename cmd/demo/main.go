package main

import (
	"fmt"

	"ontology/roll"
	_ "unsafe"
)

//go:linkname reportAdvances ontology/roll.reportAdvances
func reportAdvances(*roll.Hash) uint64

type check struct {
	name string
	ok   bool
}

var checks []check

func add(name string, ok bool) { checks = append(checks, check{name, ok}) }

func main() {
	add("skeleton", true)

	adv100k, adv1m := uint64(0), uint64(0)
	for _, c := range []struct {
		n    int
		dst  *uint64
	}{{100_000, &adv100k}, {1_000_000, &adv1m}} {
		h, _ := roll.New(16)
		for i := 0; i < c.n; i++ {
			h.Push(byte(i*7 + i>>3))
		}
		*c.dst = reportAdvances(h)
	}
	add(fmt.Sprintf("roll advances 100k=%d 1m=%d (<=len, linear)", adv100k, adv1m),
		adv100k <= 100_000 && adv1m <= 1_000_000 && adv1m > adv100k)

	failed := false
	for _, c := range checks {
		status := "OK"
		if !c.ok {
			status, failed = "FAIL", true
		}
		fmt.Printf("%-4s %s\n", status, c.name)
	}
	if failed {
		panic("demo check failed")
	}
}
