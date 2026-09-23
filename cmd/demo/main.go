package main

import (
	"fmt"
	"math/rand"
	"strings"

	"ontology/natural"
)

type check struct {
	name string
	ok   func() bool
}

var checks = []check{
	{"basic", func() bool {
		return natural.Less("file2", "file10") &&
			natural.Less("a9b", "a10a") && natural.Less("x", "x1")
	}},
	{"40-digit", func() bool {
		big9 := strings.Repeat("9", 40)
		big1 := "1" + strings.Repeat("0", 39)
		zero := strings.Repeat("0", 39) + "1"
		return natural.Compare("a"+big9, "a"+big1) > 0 &&
			natural.Compare("a"+big9, "a"+big9) == 0 &&
			natural.Compare("x"+zero, "x1") > 0
	}},
	{"leading-zeros", func() bool {
		return natural.Compare("a1", "a01") < 0 &&
			natural.Compare("a01", "a001") < 0 &&
			natural.Compare("a01b", "a1c") < 0 &&
			natural.Compare("a1b01", "a01b1") < 0 &&
			natural.Compare("x0", "x00") < 0 &&
			natural.Compare("x00", "x000") < 0 &&
			natural.Compare("x000", "x1") < 0
	}},
	{"total-order", totalOrderOK},
	{"50-shuffles", shufflesOK},
	{"non-ascii", nonASCIIOK},
	{"byte-budget", budgetOK},
}

func words() []string {
	alpha := []byte{'a', '0', '1', '9'}
	out := []string{""}
	for _, n := range []int{1, 2, 3} {
		total := 1
		for range n {
			total *= len(alpha)
		}
		for v := 0; v < total; v++ {
			b := make([]byte, n)
			x := v
			for i := n - 1; i >= 0; i-- {
				b[i] = alpha[x%len(alpha)]
				x /= len(alpha)
			}
			out = append(out, string(b))
		}
	}
	return out
}

func totalOrderOK() bool {
	w := words()
	sign := make([][]int8, len(w))
	for i := range sign {
		sign[i] = make([]int8, len(w))
		for j := range w {
			sign[i][j] = int8(natural.Compare(w[i], w[j]))
		}
	}
	for i := range w {
		for j := range w {
			if sign[i][j] != -sign[j][i] ||
				(sign[i][j] == 0) != (w[i] == w[j]) {
				return false
			}
		}
	}
	for i := range w {
		for j := range w {
			if sign[i][j] != -1 {
				continue
			}
			for k := range w {
				if sign[j][k] == -1 && sign[i][k] != -1 {
					return false
				}
			}
		}
	}
	return true
}

func shufflesOK() bool {
	base := append(words(), []string{"a1", "a01", "x0", "x00"}...)
	want := append([]string(nil), base...)
	natural.Sort(want)
	r := rand.New(rand.NewSource(42))
	for range 50 {
		got := append([]string(nil), base...)
		r.Shuffle(len(got), func(i, j int) { got[i], got[j] = got[j], got[i] })
		natural.Sort(got)
		for i := range want {
			if got[i] != want[i] {
				return false
			}
		}
	}
	return true
}

func nonASCIIOK() bool {
	a, b := "文件１２号", "文２"
	c := natural.Compare(a, b)
	if c != -natural.Compare(b, a) {
		return false
	}
	s := []string{b, a, "中10文2", "中2文10"}
	natural.Sort(s)
	return natural.Less("中2文10", "中10文2")
}

func budgetOK() bool {
	a := strings.Repeat("a", 99999) + "a"
	b := strings.Repeat("a", 99999) + "b"
	if natural.Compare(a, b) >= 0 {
		return false
	}
	return natural.BytesCompared() <= 2*(len(a)+len(b))
}

func main() {
	pass := 0
	for _, c := range checks {
		if c.ok() {
			pass++
			fmt.Println("OK  ", c.name)
			continue
		}
		fmt.Println("FAIL", c.name)
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		panic("demo checks failed")
	}
}
