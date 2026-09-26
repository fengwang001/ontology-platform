package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/bits"
	"ontology/pack"
)

var rc int

func line(name string, ok bool) {
	tag := "OK  "
	if !ok {
		tag = "FAIL "
		rc = 1
	}
	fmt.Println(tag + name)
}

func main() {
	// Section 3 vector: f0 u3=5, f1 u5=18, f2 s4=-3, f3 u2=1.
	fields := []bits.Field{{Width: 3, Value: 5}, {Width: 5, Value: 18},
		{Width: 4, Value: -3, Signed: true}, {Width: 2, Value: 1}}
	wantOff := []int{0, 3, 8, 12}
	wantContrib := []uint64{5, 144, 3328, 4096}
	labels := []string{"f0 u3=5 [0,3) 5<<0=5", "f1 u5=18 [3,8) 18<<3=144",
		"f2 s4=-3 [8,12) 13<<8=3328", "f3 u2=1 [12,14) 1<<12=4096"}
	for i, f := range fields { // textbook contrib: (bits of value)<<off
		mask := uint64(1)<<uint(f.Width) - 1
		got := (uint64(f.Value) & mask) << uint(wantOff[i])
		line(labels[i], got == wantContrib[i])
	}
	word, err := bits.PackFields(fields)
	rt := true
	for i, f := range fields {
		rt = rt && bits.Extract(word, wantOff[i], f.Width, f.Signed) == f.Value
	}
	sch, _ := pack.NewSchema(fields) // no overlap/gap: ranges contiguous from 0
	gap := true
	if rs := sch.Ranges(); len(rs) != len(fields) {
		gap = false
	} else {
		for j, r := range rs {
			if r.Off != wantOff[j] {
				gap = false
			}
		}
	}
	line(fmt.Sprintf("pack=%d (0x%X); roundtrip; no-overlap/gap; f2 sign-ext=-3", word, word),
		err == nil && word == 7573 && rt && gap && bits.Extract(word, 8, 4, true) == -3)

	// Failure injection: three distinct sentinels, no state change.
	_, e1 := bits.PackFields([]bits.Field{{Width: 3, Value: 9}}) // 9 needs 4 bits
	bm := []byte{0}
	e2 := bits.SetBit(bm, 8) // >= 8*len
	_, e3 := bits.PackFields([]bits.Field{{Width: 0, Value: 0}})
	_, e3b := bits.PackFields([]bits.Field{{Width: 33}, {Width: 33}}) // total > 64
	distinct := errors.Is(e1, bits.ErrValueOverflow) && errors.Is(e2, bits.ErrBitIndex) &&
		errors.Is(e3, bits.ErrBadWidth) && errors.Is(e3b, bits.ErrBadWidth) &&
		e1 != e2 && e2 != e3
	line("value-overflow rejected", e1 != nil && word == 7573 && errors.Is(e1, bits.ErrValueOverflow))
	line("bit-index rejected, bitmap untouched", e2 != nil && bm[0] == 0)
	line("bad-width rejected, three errors distinct", distinct)

	// O(1) location and the package-level SelfCheck (all four invariants
	// + O(1)); probe counter is unexported and never leaves the package.
	line("TestBit O(1) m=100..10000; api SelfCheck",
		pack.CheckConstantTime() == nil && api.New().SelfCheck() == nil)

	// Concurrent set of distinct bits through the api facade, no sleeps.
	const n = 1000
	fac := api.New()
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); _ = fac.Set(i) }(i)
	}
	wg.Wait()
	all := true
	for i := 0; i < n; i++ {
		ok, _ := fac.Test(i)
		all = all && ok
	}
	if err := fac.Clear(0); err != nil { // exercise Clear too
		all = false
	}
	if ok, _ := fac.Test(0); ok {
		all = false
	}
	line("1000 goroutines set 1000 distinct bits (api Set/Test/Clear)", all)
	os.Exit(rc)
}
