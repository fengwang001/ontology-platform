package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/bits"
	"ontology/bloom"
	"ontology/check"
)

func main() {
	pass, total := 0, 0
	report := func(ok bool, msg string) {
		total++
		if ok {
			pass++
			fmt.Println("OK  " + msg)
		} else {
			fmt.Println("FAIL " + msg)
		}
	}

	a := bits.New(128)
	a.Set(70)
	report(a.Get(70) && !a.Get(71) && a.Len() == 128, "bits: Set/Get/Len")

	f, err := bloom.New(10000, 0.01, 7)
	report(err == nil && f.K() > 1, "bloom: New derives m,k with k>1")

	f.Add([]byte("alice"))
	report(f.MaybeContains([]byte("alice")), "bloom: added value found")

	var empty bloom.Filter
	report(!empty.MaybeContains([]byte("x")), "bloom: empty filter says false")

	_, err = bloom.New(0, 0.01, 7)
	report(errors.Is(err, bloom.ErrBadParam), "bloom: n=0 rejected")

	_, err = bloom.New(10, 1.5, 7)
	report(errors.Is(err, bloom.ErrBadParam), "bloom: p out of (0,1) rejected")

	f.MaybeContains([]byte("probe"))
	report(f.Reads() == f.K(), "bloom: query reads exactly k bits")

	ref := check.NewSet()
	ref.Add([]byte("alice"))
	report(ref.Contains([]byte("alice")) && !ref.Contains([]byte("bob")), "check: reference set exact")

	if pass == total {
		fmt.Printf("OK   total %d/%d checks passed\n", pass, total)
	} else {
		fmt.Printf("FAIL total %d/%d checks passed\n", pass, total)
		os.Exit(1)
	}
}
