// demo 实际演练布隆过滤器的各项保证并逐条打印判定。
// 不读命令行参数、不联网；全部通过时退出码为 0。
package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"

	"ontology"
)

var failures int

func check(name, detail string, ok bool) {
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
		failures++
	}
	fmt.Printf("%s %s: %s\n", verdict, name, detail)
}

func elem(prefix string, i int) []byte {
	buf := make([]byte, 11)
	copy(buf, prefix)
	binary.BigEndian.PutUint64(buf[3:], uint64(i))
	return buf
}

func main() {
	const (
		n       = 10000
		p       = 0.01
		queries = 100000
	)

	f, err := ontology.New(n, p)
	check("derive m,k", fmt.Sprintf("m=%d k=%d (err=%v)", f.M(), f.K(), err),
		err == nil && f.M() == 95851 && f.K() == 7)

	for i := 0; i < n; i++ {
		f.Add(elem("ins", i))
	}

	neg := 0
	for i := 0; i < n; i++ {
		if !f.MayContain(elem("ins", i)) {
			neg++
		}
	}
	check("zero false negatives", fmt.Sprintf("%d/%d inserted all hit, misses=%d", n-neg, n, neg), neg == 0)

	hits := 0
	for i := 0; i < queries; i++ {
		if f.MayContain(elem("qry", i)) {
			hits++
		}
	}
	fpr := float64(hits) / queries
	check("measured FPR <= 2p", fmt.Sprintf("fpr=%.4f p=%g 2p=%g", fpr, p, 2*p), fpr >= 0 && fpr <= 2*p)

	g, _ := ontology.New(n, p)
	for _, i := range rand.New(rand.NewSource(42)).Perm(n) {
		g.Add(elem("ins", i))
	}
	check("shuffled order same bytes", fmt.Sprintf("bytes=%d equal=%v", len(f.Bytes()), bytes.Equal(f.Bytes(), g.Bytes())),
		bytes.Equal(f.Bytes(), g.Bytes()))

	r1, _ := ontology.New(1000, p)
	r1.Add(elem("ins", 7))
	r2, _ := ontology.New(1000, p)
	for i := 0; i < 1000000; i++ {
		r2.Add(elem("ins", 7))
	}
	check("1e6 repeated adds same bytes", fmt.Sprintf("equal=%v", bytes.Equal(r1.Bytes(), r2.Bytes())),
		bytes.Equal(r1.Bytes(), r2.Bytes()))

	other, _ := ontology.New(1000, p)
	_, merr := ontology.Merge(f, other)
	var pme *ontology.ParamMismatchError
	check("merge param mismatch error", fmt.Sprintf("err=%v", merr),
		errors.Is(merr, ontology.ErrParamMismatch) && errors.As(merr, &pme))

	a, _ := ontology.New(n, p)
	b, _ := ontology.New(n, p)
	combined, _ := ontology.New(n, p)
	for i := 0; i < n; i++ {
		dst := a
		if i >= n/2 {
			dst = b
		}
		dst.Add(elem("ins", i))
		combined.Add(elem("ins", i))
	}
	aBefore, bBefore := a.Bytes(), b.Bytes()
	merged, merr2 := ontology.Merge(a, b)
	mergeOK := merr2 == nil && bytes.Equal(merged.Bytes(), combined.Bytes()) &&
		bytes.Equal(a.Bytes(), aBefore) && bytes.Equal(b.Bytes(), bBefore)
	check("merge equals combined insert", fmt.Sprintf("equal=%v sources unchanged", mergeOK), mergeOK)

	est := f.EstimateCount()
	rel := math.Abs(est-n) / n
	check("estimate count within 10%", fmt.Sprintf("est=%.0f true=%d rel=%.3f", est, n, rel), rel <= 0.10)

	_, e1 := ontology.New(0, p)
	_, e2 := ontology.New(n, 0)
	_, e3 := ontology.New(n, 1)
	check("invalid params rejected", fmt.Sprintf("n=0:%v p=0:%v p=1:%v", e1, e2, e3),
		errors.Is(e1, ontology.ErrInvalidN) && errors.Is(e2, ontology.ErrInvalidP) && errors.Is(e3, ontology.ErrInvalidP))

	fmt.Printf("TOTAL: %d checks, %d failed\n", 9, failures)
	if failures > 0 {
		os.Exit(1)
	}
}
