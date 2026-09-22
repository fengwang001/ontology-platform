// Command demo exercises the in-memory HyperLogLog estimator and prints one
// OK/FAIL verdict per behavior plus a total. Run with:
//
//	go run ./cmd/demo
//
// It takes no arguments and performs no network access.
package main

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"

	"ontology"
)

func main() {
	failures := 0
	check := func(name string, ok bool, detail string) {
		tag := "OK"
		if !ok {
			tag = "FAIL"
			failures++
		}
		fmt.Printf("%s %s %s\n", tag, name, detail)
	}

	// 1. Hand-built bit patterns at p=4: register 5 records rho 3, ignores a
	// smaller rho, then saturates at 61 for an all-zero remainder.
	const p = 4
	e, _ := hll.New(p)
	mk := func(index, rem uint64) uint64 { return index | rem<<p }
	e.Add(mk(5, uint64(1)<<57)) // rho 3
	e.Add(mk(5, uint64(1)<<59)) // rho 1: must be ignored
	r3 := e.InspectRegisters()[5]
	e.Add(mk(5, 0)) // rho 61: saturation
	r61 := e.InspectRegisters()[5]
	check("hand bit patterns: reg[5] rho 3 then saturated 61",
		r3 == 3 && r61 == 61, fmt.Sprintf("(got %d then %d)", r3, r61))

	// 2. Exact small-cardinality counts at p=16 (linear counting, no hits).
	hashes100 := distinctHashes(100)
	exact := true
	var detail string
	for _, n := range []int{1, 10, 100} {
		est := buildEstimate(16, hashes100[:n])
		if est != uint64(n) {
			exact = false
		}
		detail += fmt.Sprintf("%d/%d ", est, n)
	}
	check("exact linear counting for 1/10/100", exact, "("+detail+")")

	// 3. 100k distinct hashes: relative error must be below 15%.
	big := distinctHashes(100000)
	est := buildEstimate(10, big)
	rel := absFloat(float64(est)-100000) / 100000
	check("100k distinct hashes within 15%", rel < 0.15,
		fmt.Sprintf("(estimate=%d truth=100000 relErr=%.3f)", est, rel))

	// 4. Shuffled insertion order yields a byte-identical snapshot.
	a, _ := hll.New(10)
	b, _ := hll.New(10)
	for _, h := range big {
		a.Add(h)
	}
	sh := shuffledCopy(big)
	for _, h := range sh {
		b.Add(h)
	}
	same := equalRegisters(a.InspectRegisters(), b.InspectRegisters())
	check("shuffled insert order: identical register snapshot", same, "")

	// 5. Merging estimators with different p fails and names both p values.
	lo, _ := hll.New(4)
	hi, _ := hll.New(16)
	_, err := hll.Merge(lo, hi)
	merr := err != nil && errors.Is(err, hll.ErrPrecisionMismatch)
	check("merge p=4 with p=16 returns mismatch error", merr,
		fmt.Sprintf("(%v)", err))

	// 6. Adding the same hash one million times changes nothing.
	id, _ := hll.New(12)
	h := distinctHashes(1)[0]
	id.Add(h)
	before := id.InspectRegisters()
	beforeEst := id.Estimate()
	for i := 0; i < 1_000_000; i++ {
		id.Add(h)
	}
	idem := equalRegisters(before, id.InspectRegisters()) && id.Estimate() == beforeEst
	check("repeat-add one hash 1,000,000 times is idempotent", idem,
		fmt.Sprintf("(estimate stays %d)", beforeEst))

	if failures == 0 {
		fmt.Printf("TOTAL: all 6 checks OK\n")
	} else {
		fmt.Printf("TOTAL: %d FAIL(s)\n", failures)
		os.Exit(1)
	}
}

func buildEstimate(precision int, hashes []uint64) uint64 {
	e, _ := hll.New(precision)
	for _, h := range hashes {
		e.Add(h)
	}
	return e.Estimate()
}

func distinctHashes(n int) []uint64 {
	rng := rand.New(rand.NewPCG(0x243F6A8885A308D3, 0x13198A2E03707344))
	out := make([]uint64, n)
	seen := make(map[uint64]struct{}, n)
	for i := range out {
		for {
			h := rng.Uint64()
			if _, ok := seen[h]; !ok {
				seen[h] = struct{}{}
				out[i] = h
				break
			}
		}
	}
	return out
}

func shuffledCopy(in []uint64) []uint64 {
	rng := rand.New(rand.NewPCG(42, 42^0x9E3779B97F4A7C15))
	out := append([]uint64(nil), in...)
	rng.Shuffle(len(out), func(i, j int) {
		out[i], out[j] = out[j], out[i]
	})
	return out
}

func equalRegisters(a, b []uint8) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func absFloat(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
