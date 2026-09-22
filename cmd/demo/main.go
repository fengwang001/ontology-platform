package main

import (
	"errors"
	"fmt"
	"math"
	"os"

	"ontology"
)

func main() {
	pass := 0
	check := func(name string, ok bool) {
		if ok {
			pass++
			fmt.Println("OK", name)
			return
		}
		fmt.Println("FAIL", name)
	}

	h, _ := ontology.New(0, 1, 3)
	zero, _ := ontology.BucketIndex(0, 1, 3, 0)
	boundary := 1.0 / 3.0
	bound, _ := ontology.BucketIndex(0, 1, 3, boundary)
	last, _ := ontology.BucketIndex(0, 1, 3, math.Nextafter(1, math.Inf(-1)))
	check(fmt.Sprintf("boundary buckets: lo=%d, 1/3=%d, nextbefore(hi)=%d", zero, bound, last),
		zero == 0 && bound == 1 && last == 2)

	_ = h.Add(1)
	check("hi itself increments overflow, not last bucket",
		h.Overflow() == 1 && h.Buckets()[2] == 0)

	width := 1.0 / 3.0
	sample := -10 + 2*width
	naive := ontology.NaiveBucketIndex(-10, -9, 3, sample)
	corrected, _ := ontology.BucketIndex(-10, -9, 3, sample)
	check(fmt.Sprintf("naive index=%d; corrected index=2", naive), naive == 1 && corrected == 2)

	_ = h.Add(0)
	_ = h.Add(0.5)
	_ = h.Add(math.Nextafter(1, math.Inf(-1)))
	s := h.Snapshot()
	var bucketSum uint64
	for _, count := range s.Buckets {
		bucketSum += count
	}
	check("count identity buckets+under+over+skipped=added",
		bucketSum+s.Underflow+s.Overflow+s.Skipped == s.Added)

	_ = h.Add(math.NaN())
	_ = h.Add(math.Inf(-1))
	_ = h.Add(math.Inf(1))
	check("NaN skipped; -Inf underflow; +Inf overflow",
		h.Skipped() == 1 && h.Underflow() == 1 && h.Overflow() == 2)

	other, _ := ontology.New(-1, 2, 7)
	_, err := h.Merge(other)
	check("different parameter Merge reports mismatch", errors.Is(err, ontology.ErrHistogramMismatch))

	beforeA, beforeB := h.Snapshot(), other.Snapshot()
	_, _ = h.Merge(other)
	check("Merge leaves both sources unchanged",
		fmt.Sprint(h.Snapshot().Buckets) == fmt.Sprint(beforeA.Buckets) &&
			fmt.Sprint(other.Snapshot().Buckets) == fmt.Sprint(beforeB.Buckets))

	fmt.Printf("TOTAL %d/%d checks passed\n", pass, 7)
	if pass != 7 {
		os.Exit(1)
	}
}
