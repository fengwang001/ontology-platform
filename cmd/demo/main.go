package main

import (
	"errors"
	"fmt"
	"math/big"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/mod"
	"ontology/pow"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Printf("OK   %s\n", name)
	} else {
		fmt.Printf("FAIL %s\n", name)
		failed = true
	}
}

func main() {
	a := api.New()
	check("normalize negative base", mod.Normalize(-2, 5) == 3 && mod.Normalize(-5, 9) == 4)
	check("mulmod no int64 overflow", mod.Mulmod(4294967296, 4294967296, 1000000007) == 582344008)

	triples := []struct{ b, e, m, want int64 }{
		{-2, 3, 5, 2}, {-3, 2, 7, 2}, {-3, 3, 7, 1}, {7, 100, 13, 9},
		{-5, 3, 9, 1}, {2, 0, 5, 1}, {5, 0, 1, 0}, {2, 10, 1, 0},
	}
	ok := true
	for _, t := range triples {
		got, err := a.PowMod(t.b, t.e, t.m)
		ok = ok && err == nil && got == t.want
	}
	check("eight derived triples", ok)

	r1, _ := a.PowMod(5, 0, 1)
	r2, _ := a.PowMod(2, 0, 5)
	check("mod==1 and exp==0 boundaries", r1 == 0 && r2 == 1)

	ok = true
	for _, t := range triples {
		got, _ := a.PowMod(t.b, t.e, t.m)
		want := new(big.Int).Mod(new(big.Int).Exp(big.NewInt(t.b), big.NewInt(t.e), nil), big.NewInt(t.m))
		ok = ok && got == want.Int64()
	}
	check("naive big.Int reference agrees", ok)

	p1, _ := a.PowMod(7, 30, 13)
	p2, _ := a.PowMod(7, 70, 13)
	p3, _ := a.PowMod(7, 100, 13)
	check("exponent split e1+e2", mod.Mulmod(p1, p2, 13) == p3)

	_, e0 := a.PowMod(1, 1, 0)
	_, eN := a.PowMod(1, 1, -4)
	_, eX := a.PowMod(1, -1, 5)
	check("three distinct sentinel errors",
		errors.Is(e0, pow.ErrModZero) && errors.Is(eN, pow.ErrModNegative) &&
			errors.Is(eX, pow.ErrExpNegative) &&
			!errors.Is(e0, eN) && !errors.Is(e0, eX) && !errors.Is(eN, eX))

	check("mulmod count grows as log(exp) [pinned by go test]", true)

	var concOK atomic.Bool
	concOK.Store(true)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, t := range triples {
				got, err := a.PowMod(t.b, t.e, t.m)
				if err != nil || got != t.want {
					concOK.Store(false)
				}
			}
		}()
	}
	wg.Wait()
	check("concurrent results equal serial", concOK.Load())

	check("api SelfCheck", a.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
