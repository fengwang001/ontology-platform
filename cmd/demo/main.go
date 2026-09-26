package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/dh"
	"ontology/modarith"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s: %s\n", name, status)
}

func naivePow(base, e, p uint64) uint64 {
	r := uint64(1)
	for i := uint64(0); i < e; i++ {
		r = modarith.Mul(r, base, p)
	}
	return r
}

func main() {
	// Section 3: the seven square-and-multiply steps of 2^90 mod 29.
	// After processing a prefix of 0b1011010, r equals 2^prefix mod 29.
	want := []uint64{2, 4, 3, 18, 5, 21, 6}
	ok := true
	for i, w := range want {
		prefix := uint64(0b1011010 >> (6 - i))
		if got := modarith.ModPow(2, prefix, 29); got != w {
			ok = false
		}
	}
	check("seven steps r=2,4,3,18,5,21,6", ok)

	// ModPow matches the naive multiply-e-times reference.
	ok = true
	for _, c := range [][3]uint64{{2, 90, 29}, {7, 1000, 101}, {123456, 78901, 104729}} {
		if modarith.ModPow(c[0], c[1], c[2]) != naivePow(c[0], c[1], c[2]) {
			ok = false
		}
	}
	check("modpow matches naive reference", ok)

	// dh: exchange consistency on the worked example a=5, b=11.
	gr, err := dh.NewGroup(29, 2)
	pkA, errA := gr.PublicKey(5)
	pkB, errB := gr.PublicKey(11)
	sA, errS1 := gr.Secret(5, pkB)
	sB, errS2 := gr.Secret(11, pkA)
	ok = err == nil && errA == nil && errB == nil && errS1 == nil && errS2 == nil &&
		pkA == 3 && pkB == 18 && sA == 15 && sB == 15
	check("dh exchange a=5,b=11 -> A=3,B=18,S=15", ok)

	// api: three rejection classes give three distinct sentinel errors.
	x, _ := api.New(29, 2)
	_, errGroup := api.New(1, 2)
	_, errPriv := x.PublicKey(0)
	_, errPub := x.Secret(5, 1)
	ok = errors.Is(errGroup, api.ErrBadGroupParams) &&
		errors.Is(errPriv, api.ErrBadPrivateKey) &&
		errors.Is(errPub, api.ErrBadPublicKey) &&
		!errors.Is(errGroup, api.ErrBadPrivateKey) &&
		!errors.Is(errPriv, api.ErrBadPublicKey) &&
		!errors.Is(errPub, api.ErrBadGroupParams)
	check("three distinct decidable errors", ok)

	// Rejections leave no trace: valid calls still give the same answers.
	still, _ := x.PublicKey(5)
	sStill, _ := x.Secret(5, 18)
	check("state unchanged after rejections", still == 3 && sStill == 15)

	// Concurrent Secret calls all agree with the reference value.
	const n = 32
	results := make([]uint64, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, err := x.Secret(5, 18)
			if err != nil {
				results[i] = ^uint64(0)
				return
			}
			results[i] = s
		}(i)
	}
	wg.Wait()
	ok = true
	for _, s := range results {
		if s != 15 {
			ok = false
		}
	}
	check("concurrent Secret consistent", ok)

	check("api.SelfCheck", api.SelfCheck() == nil)

	// Large exponent: e = 2^64-1 returns instantly (a linear-in-e method
	// would need ~1.8e19 multiplications) and satisfies Fermat mod 29.
	e := ^uint64(0)
	ok = modarith.ModPow(2, e, 29) == modarith.ModPow(2, e%28, 29)
	check("huge-e fast pow (mul count not linear in e)", ok)

	if failed {
		os.Exit(1)
	}
}
