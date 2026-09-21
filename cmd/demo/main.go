// 布隆过滤器演示：go run ./cmd/demo
// 不读命令行参数、不联网，全部判定通过时退出码为 0。
package main

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"

	"ontology/bloom"
)

const (
	n       = 10000
	p       = 0.01
	queries = 100000
	repeatN = 1000000
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

func elem(prefix string, i int) []byte {
	return []byte(fmt.Sprintf("%s-%d", prefix, i))
}

func main() {
	f, err := bloom.New(n, p)
	if err != nil {
		fmt.Println("FAIL New:", err)
		os.Exit(1)
	}
	report(f.M() > 0 && f.K() > 0, "params: n=%d p=%v -> m=%d bits, k=%d hashes", n, p, f.M(), f.K())

	for i := 0; i < n; i++ {
		f.Add(elem("in", i))
	}
	miss := 0
	for i := 0; i < n; i++ {
		if !f.MayContain(elem("in", i)) {
			miss++
		}
	}
	report(miss == 0, "zero false negatives: %d/%d inserted elements all hit", n-miss, n)

	hits := 0
	for i := 0; i < queries; i++ {
		if f.MayContain(elem("out", i)) {
			hits++
		}
	}
	fpr := float64(hits) / queries
	report(fpr <= 2*p, "measured FPR=%.5f on %d unseen queries (target p=%v, bound 2p=%v)", fpr, queries, p, 2*p)

	a, _ := bloom.New(n, p)
	b, _ := bloom.New(n, p)
	for i := 0; i < n; i++ {
		a.Add(elem("in", i))
	}
	for _, i := range rand.New(rand.NewSource(7)).Perm(n) {
		b.Add(elem("in", i))
	}
	report(bytes.Equal(a.Bytes(), b.Bytes()), "shuffled insertion order yields identical bytes (%d bytes)", len(a.Bytes()))

	once, _ := bloom.New(n, p)
	many, _ := bloom.New(n, p)
	once.Add(elem("dup", 0))
	for i := 0; i < repeatN; i++ {
		many.Add(elem("dup", 0))
	}
	report(bytes.Equal(once.Bytes(), many.Bytes()), "inserting same element %d times leaves bytes unchanged", repeatN)

	other, _ := bloom.New(2*n, p)
	_, merr := bloom.Merge(f, other)
	report(errors.Is(merr, bloom.ErrMismatch), "mismatched Merge rejected: %v", merr)

	left, _ := bloom.New(n, p)
	right, _ := bloom.New(n, p)
	for i := 0; i < n; i++ {
		left.Add(elem("L", i))
		right.Add(elem("R", i))
	}
	merged, _ := bloom.Merge(left, right)
	combined, _ := bloom.New(n, p)
	for i := 0; i < n; i++ {
		combined.Add(elem("L", i))
		combined.Add(elem("R", i))
	}
	report(bytes.Equal(merged.Bytes(), combined.Bytes()), "Merge result identical to combined insert")

	est := merged.EstimateCount()
	rel := math.Abs(est-2*n) / (2 * n)
	report(rel <= 0.10, "EstimateCount=%.0f vs true %d (rel err %.2f%%)", est, 2*n, rel*100)

	_, ierr1 := bloom.New(0, p)
	_, ierr2 := bloom.New(n, 0)
	_, ierr3 := bloom.New(n, 1)
	badOK := errors.Is(ierr1, bloom.ErrInvalidParam) && errors.Is(ierr2, bloom.ErrInvalidParam) && errors.Is(ierr3, bloom.ErrInvalidParam)
	report(badOK, "invalid params rejected: n=0 / p=0 / p=1 all return ErrInvalidParam")

	fmt.Printf("TOTAL %d checks, %d failed\n", 9, failures)
	if failures > 0 {
		os.Exit(1)
	}
}
