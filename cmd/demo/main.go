package main

import (
	"fmt"
	"math/rand"
	"strings"

	"ontology/natural"
)

type pair struct{ a, b string }

func expectLess(tt []pair) bool {
	for _, t := range tt {
		if natural.Compare(t.a, t.b) >= 0 {
			return false
		}
	}
	return true
}

func alphabetStrings(alpha string, max int) []string {
	out := []string{""}
	prev := []string{""}
	for n := 1; n <= max; n++ {
		cur := make([]string, 0, len(prev)*len(alpha))
		for _, p := range prev {
			for _, c := range alpha {
				cur = append(cur, p+string(c))
			}
		}
		out, prev = append(out, cur...), cur
	}
	return out
}

func totalOrder() bool {
	set := alphabetStrings("a019", 3)
	rank := make(map[string]int, len(set))
	sorted := append([]string(nil), set...)
	natural.Sort(sorted)
	for i, s := range sorted {
		rank[s] = i
	}
	for _, x := range set {
		if natural.Compare(x, x) != 0 || (x == x) != (rank[x] == rank[x]) {
			return false
		}
		for _, y := range set {
			c := natural.Compare(x, y)
			if -natural.Compare(y, x) != c || (c < 0) != (rank[x] < rank[y]) {
				return false
			}
			if c == 0 != (x == y) {
				return false
			}
		}
	}
	return true
}

func shuffleStable() bool {
	base := []string{"a1", "a01", "a001", "a10", "a2", "x0", "x00", "x1", "file2", "file10", "b", "b0", "b00"}
	want := append([]string(nil), base...)
	natural.Sort(want)
	rng := rand.New(rand.NewSource(1))
	for s := 0; s < 50; s++ {
		got := append([]string(nil), base...)
		rng.Shuffle(len(got), func(i, j int) { got[i], got[j] = got[j], got[i] })
		natural.Sort(got)
		if strings.Join(got, "|") != strings.Join(want, "|") {
			return false
		}
	}
	return true
}

func nonASCII() (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	natural.Compare("１２a", "１２b")
	natural.Compare("é9", "é10")
	return natural.Compare("１２a", "１２b") < 0
}

func bigNumber() bool {
	return expectLess([]pair{
		{"x" + strings.Repeat("9", 40), "x1" + strings.Repeat("0", 40)},
		{"a0000000000000000000000000000000000000001", "a2"},
	})
}

func counterBound() bool {
	a := strings.Repeat("a", 100000)
	b := a[:99999] + "b"
	_ = natural.Compare(a, b)
	return natural.ComparedBytes() <= 2*(len(a)+len(b))
}

func main() {
	fails := 0
	report := func(name string, ok bool) {
		tag := "OK"
		if !ok {
			tag, fails = "FAIL", fails+1
		}
		fmt.Printf("%s %s\n", tag, name)
	}

	report("basic", expectLess([]pair{{"file2", "file10"}, {"a9b", "a10a"}, {"x", "x1"}}))
	report("40-digit-no-overflow", bigNumber())
	report("leading-zeros", expectLess([]pair{
		{"a1", "a01"}, {"a01", "a001"}, {"a01b", "a1c"},
		{"a1b01", "a01b1"}, {"x0", "x00"}, {"x00", "x000"}, {"x000", "x1"},
	}))
	report("total-order-exhaustive", totalOrder())
	report("sort-stable-50-shuffles", shuffleStable())
	report("non-ascii-no-panic", nonASCII())
	report("checked-bytes-bound", counterBound())

	if fails == 0 {
		fmt.Println("ALL 7 OK")
	} else {
		fmt.Printf("%d CHECK(S) FAILED\n", fails)
	}
}
