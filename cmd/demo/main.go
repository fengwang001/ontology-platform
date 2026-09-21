// Command demo exercises the RLE bitmap end to end and prints one
// OK/FAIL line per check plus a final summary. It takes no flags,
// uses no network, and exits 0 when every check passes.
package main

import (
	"bytes"
	"fmt"
	"math"
	"math/rand"
	"os"
	"sync"

	"ontology/bitmap"
)

var failures int

func check(name string, ok bool, detail string) {
	status := "OK  "
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %-22s %s\n", status, name, detail)
}

func main() {
	// 1. Four construction paths, one identical encoding.
	perBit := bitmap.New()
	for _, x := range []uint32{1, 2, 3} {
		perBit.Set(x)
	}
	for x := uint32(100); x <= 199; x++ {
		perBit.Set(x)
	}
	perBit.Set(5000)
	batch := bitmap.New()
	batch.SetRange(1, 3)
	batch.SetRange(100, 199)
	batch.Set(5000)
	u1, u2 := bitmap.New(), bitmap.New()
	u1.SetRange(1, 3)
	u1.Set(5000)
	u2.SetRange(100, 199)
	viaUnion, _ := u1.Union(u2)
	shrunk := bitmap.New()
	shrunk.SetRange(0, 6000)
	shrunk.ClearRange(0, 0)
	shrunk.ClearRange(4, 99)
	shrunk.ClearRange(200, 6000)
	shrunk.Set(5000) // re-add a bit cleared above
	ref := perBit.Bytes()
	same := bytes.Equal(batch.Bytes(), ref) &&
		bytes.Equal(viaUnion.Bytes(), ref) &&
		bytes.Equal(shrunk.Bytes(), ref)
	check("4 paths same bytes", same, fmt.Sprintf("encoding=%v", ref))
	// 2. Set then Clear restores the original encoding.
	b := bitmap.New()
	b.SetRange(10, 20)
	before := b.Bytes()
	b.Set(500)
	b.Clear(500)
	check("set+clear restores", bytes.Equal(b.Bytes(), before),
		fmt.Sprintf("bytes=%v", b.Bytes()))
	// 3. Empty set encoding: deterministic and shortest (length 0).
	check("empty set encoding", len(bitmap.New().Bytes()) == 0, "len=0")
	// 4. MaxUint32 can be set without overflow.
	top := bitmap.New()
	top.Set(math.MaxUint32)
	maxV, _ := top.Max()
	minV, _ := top.Min()
	check("MaxUint32 set", top.Contains(math.MaxUint32) &&
		top.Count() == 1 && maxV == math.MaxUint32 && minV == math.MaxUint32,
		fmt.Sprintf("count=%d min=%d max=%d", top.Count(), minV, maxV))
	// 5. Intersect two 2-run sets covering 10M bits: few run steps.
	a := bitmap.New()
	a.SetRange(1_000_000, 5_999_999)
	c := bitmap.New()
	c.SetRange(3_000_000, 7_999_999)
	inter, steps := a.Intersect(c)
	check("10M-bit 2-run intersect", inter.Count() == 3_000_000 && steps < 10,
		fmt.Sprintf("count=%d steps=%d", inter.Count(), steps))
	// 6. Differential spot check against a naive per-bit set.
	check("naive differential", naiveCheck(), "200 random ops, 3 ops compared")
	// 7. Concurrent Set/Clear leaves a verifiable, exact bitmap.
	check("concurrent verify", concurrentCheck(), "8 setters x 2000 bits")
	total := 7
	fmt.Printf("SUMMARY %d/%d checks passed\n", total-failures, total)
	if failures > 0 {
		os.Exit(1)
	}
}

// naiveCheck compares Union/Intersect/Difference against a naive
// map-based set on random inputs.
func naiveCheck() bool {
	rng := rand.New(rand.NewSource(7))
	const size = 1 << 14
	build := func() (*bitmap.Bitmap, map[uint32]bool) {
		bm := bitmap.New()
		n := map[uint32]bool{}
		for i := 0; i < 100; i++ {
			x := rng.Uint32() % size
			if rng.Intn(2) == 0 {
				bm.Set(x)
				n[x] = true
			} else {
				bm.Clear(x)
				delete(n, x)
			}
		}
		return bm, n
	}
	ba, na := build()
	bb, nb := build()
	counts := func(f func(bool, bool) bool) uint64 {
		var c uint64
		for x := uint32(0); x < size; x++ {
			if f(na[x], nb[x]) {
				c++
			}
		}
		return c
	}
	u, _ := ba.Union(bb)
	i, _ := ba.Intersect(bb)
	d, _ := ba.Difference(bb)
	return u.Count() == counts(func(x, y bool) bool { return x || y }) &&
		i.Count() == counts(func(x, y bool) bool { return x && y }) &&
		d.Count() == counts(func(x, y bool) bool { return x && !y }) &&
		u.Verify() == nil && i.Verify() == nil && d.Verify() == nil
}

// concurrentCheck runs concurrent setters/clearers/readers and then
// verifies the structure and the exact cardinality.
func concurrentCheck() bool {
	bm := bitmap.New()
	const setters, per = 8, 2000
	var wg sync.WaitGroup
	for s := 0; s < setters; s++ {
		base := uint32(s * per)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := uint32(0); i < per; i++ {
				bm.Set(base + i)
				bm.Clear(base + i + setters*per) // never-set region
			}
		}()
	}
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				_ = bm.Bytes()
				_ = bm.Count()
			}
		}
	}()
	wg.Wait()
	close(done)
	return bm.Verify() == nil && bm.Count() == setters*per
}
