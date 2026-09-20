// Command demo exercises the weighted reservoir sampler end to end and
// prints one OK/FAIL verdict line per check, plus a final summary.
// It takes no arguments, uses no network, and exits with code 0 when
// every check passes.
package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"slices"

	"ontology"
)

var failures int

func check(name string, ok bool, detail string) {
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
		failures++
	}
	fmt.Printf("%s %s: %s\n", verdict, name, detail)
}

func mustNew(k int, seed uint64) *ontology.Reservoir {
	r, err := ontology.New(k, seed)
	if err != nil {
		fmt.Println("FAIL setup:", err)
		os.Exit(1)
	}
	return r
}

func feed(r *ontology.Reservoir, n int, weight func(i int) float64) {
	for i := 0; i < n; i++ {
		if err := r.Add(fmt.Sprintf("item-%06d", i), weight(i)); err != nil {
			fmt.Println("FAIL setup:", err)
			os.Exit(1)
		}
	}
}

func main() {
	// 1. Same seed, same input: identical samples and draw counts.
	a, b := mustNew(50, 42), mustNew(50, 42)
	feed(a, 5000, func(int) float64 { return 3 })
	feed(b, 5000, func(int) float64 { return 3 })
	check("same-seed reproducible",
		slices.Equal(a.Sample(), b.Sample()) && a.RandConsumed() == b.RandConsumed(),
		fmt.Sprintf("50 elements identical, %d draws each", a.RandConsumed()))

	// 2. Different seeds produce different samples.
	c := mustNew(50, 43)
	feed(c, 5000, func(int) float64 { return 3 })
	check("different-seed differs", !slices.Equal(a.Sample(), c.Sample()),
		"seed 42 vs 43 on identical input")

	// 3. Inclusion frequency is proportional to weight (k=1, 2000 rounds).
	weights := []float64{1, 2, 3}
	counts := make([]int, 3)
	for round := 0; round < 2000; round++ {
		r := mustNew(1, 0xC0FFEE+uint64(round))
		for i, w := range weights {
			_ = r.Add(string(rune('A'+i)), w)
		}
		counts[r.Sample()[0][0]-'A']++
	}
	ok, detail := true, ""
	for i, w := range weights {
		got, want := float64(counts[i])/2000, w/6
		if math.Abs(got-want) > 0.06 {
			ok = false
		}
		detail += fmt.Sprintf("w=%v %.3f(want %.3f) ", w, got, want)
	}
	check("frequency proportional to weight", ok, detail)

	// 4. k <= 0 returns a detectable error.
	_, err := ontology.New(0, 1)
	check("k<=0 rejected", errors.Is(err, ontology.ErrInvalidCapacity), fmt.Sprint(err))

	// 5. Stream shorter than k returns everything.
	short := mustNew(10, 5)
	feed(short, 4, func(int) float64 { return 1 })
	check("short stream returns all", len(short.Sample()) == 4,
		fmt.Sprintf("4 of 4 elements kept (k=10)"))

	// 6. Invalid weights are rejected and counted.
	bad := mustNew(5, 11)
	for _, w := range []float64{0, -1, 1.5} {
		_ = bad.Add("bad", w)
	}
	_ = bad.Add("good", 2)
	check("invalid weights rejected",
		bad.Rejected() == 3 && bad.Total() == 1 && bad.Len() == 1,
		fmt.Sprintf("rejected=%d accepted=%d len=%d", bad.Rejected(), bad.Total(), bad.Len()))

	// 7. Million-element stream: reservoir never exceeds k.
	big := mustNew(32, 17)
	within := true
	for i := 0; i < 1_000_000; i++ {
		_ = big.Add(fmt.Sprintf("e%d", i), 1+float64(i%7))
		if big.Len() > 32 {
			within = false
		}
	}
	check("million-stream O(k) memory", within && big.Len() == 32,
		fmt.Sprintf("len=%d after %d adds", big.Len(), big.Total()))

	// 8. Sample is idempotent: no randomness at read time.
	check("sample idempotent", slices.Equal(big.Sample(), big.Sample()),
		"two consecutive Sample calls identical")

	if failures == 0 {
		fmt.Println("SUMMARY: all 8 checks passed")
	} else {
		fmt.Printf("SUMMARY: %d of 8 checks failed\n", failures)
		os.Exit(1)
	}
}
