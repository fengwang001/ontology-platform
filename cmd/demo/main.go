// Command demo exercises the in-process HyperLogLog estimator and prints one
// OK/FAIL verdict per scenario, followed by a summary line. It takes no
// arguments and performs no network access.
package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"

	hll "ontology"
)

func splitmix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

func distinctHashes(n int) []uint64 {
	out := make([]uint64, 0, n)
	seen := make(map[uint64]struct{}, n)
	for x := uint64(0); len(out) < n; x++ {
		h := splitmix64(x)
		if _, ok := seen[h]; ok {
			continue
		}
		seen[h] = struct{}{}
		out = append(out, h)
	}
	return out
}

func verdict(ok bool, format string, args ...any) bool {
	tag := "OK  "
	if !ok {
		tag = "FAIL"
	}
	fmt.Printf("%s %s\n", tag, fmt.Sprintf(format, args...))
	return ok
}

func main() {
	failures := 0
	check := func(ok bool, format string, args ...any) {
		if !verdict(ok, format, args...) {
			failures++
		}
	}

	// 1) Hand-built bit patterns -> exact register ranks.
	e, _ := hll.New(4)
	e.Add(uint64(1)<<63 | 0x1) // idx 1, rho 1
	e.Add(uint64(1)<<61 | 0x1) // idx 1, rho 3 (replaces)
	e.Add(uint64(1)<<62 | 0x1) // idx 1, rho 2 (ignored)
	e.Add(0x5)                 // idx 5, all-zero high -> rho 61
	regs := e.InspectRegisters()
	check(regs[1] == 3 && regs[5] == 61,
		"hand-crafted registers: M[1]=%d (want 3), M[5]=%d (want 61)",
		regs[1], regs[5])

	// 2) Exact counts for 1/10/100 distinct hashes.
	hs := distinctHashes(100)
	exact := true
	detail := ""
	for _, n := range []int{1, 10, 100} {
		s, _ := hll.New(10)
		for _, h := range hs[:n] {
			s.Add(h)
		}
		got := s.Estimate()
		detail += fmt.Sprintf(" %d/%d", got, n)
		if int(got) != n {
			exact = false
		}
	}
	check(exact, "exact small-cardinality counts (estimate/true):%s", detail)

	// 3) 100k distinct hashes within 15% relative error.
	big, _ := hll.New(14)
	for _, h := range distinctHashes(100000) {
		big.Add(h)
	}
	est := float64(big.Estimate())
	rel := math.Abs(est-100000) / 100000
	check(rel < 0.15, "100k distinct hashes: estimate=%.0f true=100000 relErr=%.3f%%",
		est, rel*100)

	// 4) Shuffled insertion order -> byte-identical registers.
	order := distinctHashes(5000)
	fwd, _ := hll.New(12)
	for _, h := range order {
		fwd.Add(h)
	}
	shuf := append([]uint64(nil), order...)
	r := rand.New(rand.NewSource(1))
	r.Shuffle(len(shuf), func(i, j int) {
		shuf[i], shuf[j] = shuf[j], shuf[i]
	})
	rev, _ := hll.New(12)
	for _, h := range shuf {
		rev.Add(h)
	}
	same := true
	a, b := fwd.InspectRegisters(), rev.InspectRegisters()
	for i := range a {
		if a[i] != b[i] {
			same = false
		}
	}
	check(same && fwd.Estimate() == rev.Estimate(),
		"shuffled insertion: registers byte-identical, estimate=%d", fwd.Estimate())

	// 5) Merge with mismatched p reports both precision values.
	p4, _ := hll.New(4)
	p16, _ := hll.New(16)
	_, err := hll.Merge(p4, p16)
	var pe hll.PrecisionMismatchError
	reported := err != nil && errors.As(err, &pe) && pe.P1 == 4 && pe.P2 == 16
	check(reported, "merge p=4 with p=16 rejected: %v", err)

	// 6) One hash added 1,000,000 times: estimate and registers unchanged.
	idm, _ := hll.New(8)
	h := uint64(0xDEADBEEFCAFEBABE)
	idm.Add(h)
	before := idm.InspectRegisters()
	for i := 0; i < 1_000_000; i++ {
		idm.Add(h)
	}
	after := idm.InspectRegisters()
	idem := idm.Estimate() == 1
	for i := range before {
		if before[i] != after[i] {
			idem = false
		}
	}
	check(idem, "same hash x1,000,000: estimate=%d (want 1), registers unchanged",
		idm.Estimate())

	if failures == 0 {
		fmt.Println("OK  summary: 6/6 scenarios passed")
		os.Exit(0)
	}
	fmt.Printf("FAIL summary: %d/6 scenarios failed\n", failures)
	os.Exit(1)
}
