package main

import (
	"errors"
	"fmt"

	"ontology/id"
	"ontology/uf"
)

func main() {
	type probe struct {
		name string
		ok   bool
	}
	set := uf.New(5)
	merged1, _ := set.Union(0, 1)
	merged2, _ := set.Union(1, 2)
	mergedAgain, _ := set.Union(0, 2)
	transitive, _ := set.Connected(0, 2)
	separate, _ := set.Connected(0, 3)

	typed := id.New(3)
	_, negErr := typed.Find(-1)
	_, bigErr := typed.Union(0, 9)
	_, badErr := set.Find(9)

	probes := []probe{
		{"New(0) count is zero", uf.New(0).Count() == 0},
		{"New(1) self connected", selfConnected()},
		{"union merges distinct sets", merged1 && merged2 && !mergedAgain},
		{"equivalence is transitive", transitive && !separate},
		{"count tracks components", set.Count() == 3},
		{"typed negative index error", errors.Is(negErr, id.ErrNegative)},
		{"typed oversized index error", errors.Is(bigErr, id.ErrTooLarge) && errors.Is(badErr, uf.ErrBadIndex)},
	}

	fails := 0
	for _, p := range probes {
		check(p.name, p.ok, &fails)
	}
	fmt.Printf("total: %d check(s), %d fail(s)\n", len(probes), fails)
	if fails > 0 {
		panic("demo checks failed")
	}
}

func selfConnected() bool {
	one := uf.New(1)
	ok, err := one.Connected(0, 0)
	return err == nil && ok
}

func check(name string, ok bool, fails *int) {
	if ok {
		fmt.Printf("OK   %s\n", name)
		return
	}
	*fails++
	fmt.Printf("FAIL %s\n", name)
}
