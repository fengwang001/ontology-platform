package main

import (
	"fmt"
	"math/rand"
	"os"
	"strings"

	"ontology/natural"
)

type check struct {
	name string
	ok   bool
}

func main() {
	var results []check

	results = append(results, check{"basic", basicOK()})
	results = append(results, check{"bignum40", bigNumOK()})
	results = append(results, check{"leading-zero", leadingZeroOK()})
	results = append(results, check{"total-order triples", totalOrderOK()})
	results = append(results, check{"50 shuffles", shuffleOK()})
	results = append(results, check{"non-ascii", nonASCIIOK()})
	results = append(results, check{"byte counter", counterOK()})

	failed := 0
	for _, r := range results {
		status := "OK"
		if !r.ok {
			status = "FAIL"
			failed++
		}
		fmt.Printf("%s\t%s\n", status, r.name)
	}
	fmt.Printf("total: %d, failed: %d\n", len(results), failed)
	if failed > 0 {
		os.Exit(1)
	}
}

func expectLess(want bool, pairs ...[2]string) bool {
	for _, p := range pairs {
		if got := natural.Less(p[0], p[1]); got != want {
			return false
		}
		if natural.Compare(p[0], p[1]) == natural.Compare(p[1], p[0]) && p[0] != p[1] {
			return false
		}
	}
	return true
}

func basicOK() bool {
	return expectLess(true,
		[2]string{"file2", "file10"},
		[2]string{"a9b", "a10a"},
		[2]string{"x", "x1"},
	)
}

func bigNumOK() bool {
	big := strings.Repeat("9", 40)
	bigger := "1" + strings.Repeat("0", 40)
	return natural.Less(big, bigger) && natural.Less(strings.Repeat("0", 40)+"1", strings.Repeat("0", 39)+"2")
}

func leadingZeroOK() bool {
	chain := []string{"a1", "a01", "a001", "a01b", "a1b01", "a01b1", "a1c",
		"x0", "x00", "x000", "x1"}
	for i := 0; i+1 < len(chain); i++ {
		if !natural.Less(chain[i], chain[i+1]) {
			return false
		}
	}
	return natural.Compare("a01", "a01") == 0
}

func gen(alpha []byte, maxLen int) []string {
	out := []string{""}
	for l := 1; l <= maxLen; l++ {
		n := pow(len(alpha), l)
		for v := 0; v < n; v++ {
			buf := make([]byte, l)
			x := v
			for i := l - 1; i >= 0; i-- {
				buf[i] = alpha[x%len(alpha)]
				x /= len(alpha)
			}
			out = append(out, string(buf))
		}
	}
	return out
}

func pow(b, e int) int {
	r := 1
	for i := 0; i < e; i++ {
		r *= b
	}
	return r
}

func totalOrderOK() bool {
	ss := gen([]byte{'a', '0', '1'}, 4)
	cmp := func(i, j int) int { return natural.Compare(ss[i], ss[j]) }
	for i := range ss {
		if cmp(i, i) != 0 {
			return false
		}
		for j := range ss {
			c := cmp(i, j)
			if c+cmp(j, i) != 0 {
				return false // antisymmetry
			}
			if (c == 0) != (ss[i] == ss[j]) {
				return false
			}
		}
	}
	for i := range ss {
		for j := range ss {
			for k := range ss {
				if cmp(i, j) < 0 && cmp(j, k) < 0 && !(cmp(i, k) < 0) {
					return false
				}
			}
		}
	}
	return true
}

func shuffleOK() bool {
	base := []string{"file2", "file10", "a1", "a01", "a001", "a9b", "a10a",
		"x0", "x00", "x1", "z", "a1b01", "a01b1", "", "0", "00", "a01b", "a1c"}
	var ref []string
	r := rand.New(rand.NewSource(1))
	for t := 0; t < 50; t++ {
		s := append([]string(nil), base...)
		r.Shuffle(len(s), func(i, j int) { s[i], s[j] = s[j], s[i] })
		natural.Sort(s)
		if ref == nil {
			ref = s
		} else if strings.Join(s, "|") != strings.Join(ref, "|") {
			return false
		}
	}
	return len(ref) == len(base)
}

func nonASCIIOK() bool {
	ss := []string{"é1", "é10", "文２", "文１２", "a\ufffd0", string([]byte{0xff, '9'})}
	for i := 0; i < len(ss); i++ {
		for j := 0; j < len(ss); j++ {
			natural.Compare(ss[i], ss[j])
		}
	}
	return natural.Less("é1", "é10")
}

func counterOK() bool {
	a := strings.Repeat("a", 100000) + "1"
	b := strings.Repeat("a", 100000) + "2"
	natural.Compare(a, b)
	return natural.Checked() <= 2*(len(a)+len(b))
}
