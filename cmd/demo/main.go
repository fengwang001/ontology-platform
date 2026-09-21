// Command demo exercises the RLE bitmap set end to end and prints one
// OK/FAIL verdict per check. It takes no arguments and uses no network.
package main

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"sync"

	"ontology"
)

var failures int

func report(ok bool, format string, args ...any) {
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", verdict, fmt.Sprintf(format, args...))
}

// referenceSet builds {1,2,3,100} ∪ [10,19] ∪ [1000,2000] one bit at a time.
func referenceSet() *ontology.Bitmap {
	b := ontology.New()
	for _, v := range []uint32{1, 2, 3, 100} {
		b.Set(v)
	}
	for v := uint32(10); v <= 19; v++ {
		b.Set(v)
	}
	for v := uint32(1000); v <= 2000; v++ {
		b.Set(v)
	}
	return b
}

func checkUniqueEncoding() {
	want := referenceSet().Bytes()
	bulk := ontology.New()
	bulk.SetRange(1000, 2000)
	bulk.SetRange(10, 19)
	for _, v := range []uint32{1, 2, 3, 100} {
		bulk.Set(v)
	}
	a, c := ontology.New(), ontology.New()
	a.SetRange(10, 19)
	a.Set(1)
	a.Set(2)
	c.SetRange(1000, 2000)
	c.Set(3)
	c.Set(100)
	viaUnion, _ := a.Union(c)
	viaClear := referenceSet()
	for _, v := range []uint32{0, 4, 500, 999, 2001, 3000} {
		viaClear.Set(v)
	}
	for _, v := range []uint32{0, 4, 500, 999, 2001, 3000} {
		viaClear.Clear(v)
	}
	ok := bytes.Equal(bulk.Bytes(), want) &&
		bytes.Equal(viaUnion.Bytes(), want) &&
		bytes.Equal(viaClear.Bytes(), want)
	report(ok, "four construction paths yield identical bytes (%d bytes)", len(want))
}

func checkSetThenClear() {
	b := referenceSet()
	before := b.Bytes()
	b.Set(7)
	b.Clear(7)
	report(bytes.Equal(b.Bytes(), before), "set-then-clear restores original encoding")
}

func checkEmpty() {
	report(bytes.Equal(ontology.New().Bytes(), []byte{0x00}), "empty set encodes as [0x00]")
}

func checkMaxUint32() {
	b := ontology.New()
	b.Set(math.MaxUint32)
	lo, _ := b.Min()
	hi, _ := b.Max()
	ok := b.Count() == 1 && lo == math.MaxUint32 && hi == math.MaxUint32
	report(ok, "MaxUint32 set: Count=%d Min=%d Max=%d", b.Count(), lo, hi)
}

func checkHugeIntersect() {
	a, b := ontology.New(), ontology.New()
	a.SetRange(1_000_000, 5_999_999)
	b.SetRange(4_000_000, 8_999_999)
	in, st := a.Intersect(b)
	ok := in.Count() == 2_000_000 && st.Steps < 10
	report(ok, "10M-bit 2-run intersect: count=%d steps=%d", in.Count(), st.Steps)
}

func checkNaiveAgreement() {
	const universe = 3000
	x, y := ontology.New(), ontology.New()
	naive := make(map[uint32]bool)
	for v := uint32(0); v < universe; v += 3 {
		x.Set(v)
	}
	for v := uint32(0); v < universe; v += 5 {
		y.Set(v)
	}
	for v := uint32(0); v < universe; v++ {
		if v%3 == 0 && v%5 == 0 {
			naive[v] = true
		}
	}
	in, _ := x.Intersect(y)
	ok := in.Count() == uint64(len(naive))
	for v := uint32(0); v < universe && ok; v++ {
		ok = in.Contains(v) == naive[v]
	}
	report(ok, "intersect agrees with naive bitmap over %d bits", universe)
}

func checkConcurrency() {
	b := ontology.New()
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			base := uint32(w * 1000)
			for i := uint32(0); i < 500; i++ {
				b.Set(base + i)
			}
			for i := uint32(250); i < 500; i++ {
				b.Clear(base + i)
			}
		}(w)
	}
	wg.Wait()
	ok := b.Verify() == nil && b.Count() == 8*250
	report(ok, "after concurrent Set/Clear: Verify passed, Count=%d", b.Count())
}

func main() {
	checkUniqueEncoding()
	checkSetThenClear()
	checkEmpty()
	checkMaxUint32()
	checkHugeIntersect()
	checkNaiveAgreement()
	checkConcurrency()
	fmt.Printf("TOTAL %d checks, %d failed\n", 7, failures)
	if failures > 0 {
		os.Exit(1)
	}
}
