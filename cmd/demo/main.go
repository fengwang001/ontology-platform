// Command demo exercises the RLE bitmap set end to end and prints one
// OK/FAIL verdict per check, plus a final summary line.
package main

import (
	"bytes"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"sync"

	"ontology"
)

var failures int

func check(name, detail string, ok bool) {
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
		failures++
	}
	fmt.Printf("%s %s %s\n", verdict, name, detail)
}

func main() {
	canonicalPaths()
	idempotentRestore()
	emptyEncoding()
	maxUint32()
	hugeRunIntersect()
	naiveSpotCheck()
	concurrentVerify()

	if failures > 0 {
		fmt.Printf("SUMMARY: %d check(s) FAILED\n", failures)
		os.Exit(1)
	}
	fmt.Println("SUMMARY: all checks passed")
}

// target builds the reference set {1,2,3, 7, 100..199, 500}.
func target() *ontology.Set {
	s := ontology.New()
	for _, b := range []uint32{1, 2, 3, 7, 500} {
		s.Set(b)
	}
	s.SetRange(100, 199)
	return s
}

func canonicalPaths() {
	p1 := target()

	p2 := ontology.New() // same bits, reverse order, one by one
	p2.Set(500)
	for b := uint32(199); b >= 100; b-- {
		p2.Set(b)
	}
	for _, b := range []uint32{7, 3, 2, 1} {
		p2.Set(b)
	}

	a, b := ontology.New(), ontology.New() // path 3: union of two sets
	a.SetRange(1, 3)
	a.Set(7)
	b.SetRange(100, 199)
	b.Set(500)
	p3, _ := a.Union(b)

	p4 := ontology.New() // path 4: set too many, clear the extras
	p4.SetRange(0, 600)
	for x := uint32(0); x <= 600; x++ {
		keep := x >= 1 && x <= 3 || x == 7 || x >= 100 && x <= 199 || x == 500
		if !keep {
			p4.Clear(x)
		}
	}

	same := bytes.Equal(p1.Bytes(), p2.Bytes()) &&
		bytes.Equal(p1.Bytes(), p3.Bytes()) &&
		bytes.Equal(p1.Bytes(), p4.Bytes())
	check("canonical-encoding", "4 construction paths, byte-identical", same)
}

func idempotentRestore() {
	s := target()
	orig := s.Bytes()
	s.Set(7)    // already set
	s.Clear(42) // already clear
	s.Set(42)   // then undo
	s.Clear(42)
	s.SetRange(4, 6) // bridge 1..7 into one run, then undo
	s.SetRange(4, 6)
	s.Clear(4)
	s.Clear(5)
	s.Clear(6)
	check("set-clear-restore", "encoding byte-identical after undo", bytes.Equal(orig, s.Bytes()))
}

func emptyEncoding() {
	s := ontology.New()
	check("empty-encoding", fmt.Sprintf("len=%d (zero bytes, shortest)", len(s.Bytes())),
		s.Bytes() != nil && len(s.Bytes()) == 0)
}

func maxUint32() {
	s := ontology.New()
	s.Set(math.MaxUint32)
	s.Set(0)
	st := s.Stats()
	ok := s.Contains(math.MaxUint32) && st.Count == 2 && st.Min == 0 && st.Max == math.MaxUint32
	check("max-uint32", fmt.Sprintf("count=%d min=%d max=%d", st.Count, st.Min, st.Max), ok)
}

func hugeRunIntersect() {
	a, b := ontology.New(), ontology.New()
	a.SetRange(0, 4_999_999)
	a.SetRange(6_000_000, 10_999_999)
	b.SetRange(2_500_000, 7_499_999)
	b.SetRange(8_500_000, 13_499_999)
	inter, steps := a.Intersect(b)
	ok := inter.Count() == 6_500_000 && steps < 10 && inter.Verify() == nil
	check("huge-run-intersect",
		fmt.Sprintf("20M bits in 2+2 runs: count=%d steps=%d", inter.Count(), steps), ok)
}

func naiveSpotCheck() {
	const limit = 3000
	rng := rand.New(rand.NewPCG(11, 22))
	s1, s2 := ontology.New(), ontology.New()
	var n1, n2 [limit]bool
	for range 40 {
		lo, hi := rng.Uint32N(limit), rng.Uint32N(limit)
		if lo > hi {
			lo, hi = hi, lo
		}
		s1.SetRange(lo, hi)
		for x := lo; x <= hi; x++ {
			n1[x] = true
		}
		lo, hi = rng.Uint32N(limit), rng.Uint32N(limit)
		if lo > hi {
			lo, hi = hi, lo
		}
		s2.SetRange(lo, hi)
		for x := lo; x <= hi; x++ {
			n2[x] = true
		}
	}
	u, _ := s1.Union(s2)
	in, _ := s1.Intersect(s2)
	df, _ := s1.Difference(s2)
	ok := true
	for x := uint32(0); x < limit; x++ {
		ok = ok && u.Contains(x) == (n1[x] || n2[x]) &&
			in.Contains(x) == (n1[x] && n2[x]) &&
			df.Contains(x) == (n1[x] && !n2[x])
	}
	check("naive-spot-check", "union/intersect/difference match per-bit oracle", ok)
}

func concurrentVerify() {
	s := ontology.New()
	var wg sync.WaitGroup
	for w := range 8 {
		wg.Add(1)
		go func(base uint32) {
			defer wg.Done()
			s.SetRange(base, base+999)
			for i := uint32(0); i < 200; i++ {
				s.Clear(base + 800 + i)
			}
		}(uint32(w) * 1000)
	}
	wg.Wait()
	ok := s.Verify() == nil && s.Count() == 8*800
	check("concurrent-verify", fmt.Sprintf("count=%d verify-passed=%v", s.Count(), s.Verify() == nil), ok)
}
