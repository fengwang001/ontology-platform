// Command demo prints OK/FAIL for each semantic guarantee of the
// natural ordering and exits non-zero if any check fails.
package main

import (
	"fmt"
	"math/rand"
	"os"
	"slices"
	"strings"

	"ontology/natural"
)

var failed int

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed++
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	check("basic", natural.Compare("file2", "file10") < 0 &&
		natural.Compare("a9b", "a10a") < 0 &&
		natural.Compare("x", "x1") < 0)
	big1 := "f" + strings.Repeat("9", 40)
	big2 := "f1" + strings.Repeat("0", 40)
	check("bignum-40-digit", natural.Compare(big1, big2) < 0 &&
		natural.Compare(big2, big1) > 0)
	check("leading-zeros", natural.Compare("a1", "a01") < 0 &&
		natural.Compare("a01", "a001") < 0 &&
		natural.Compare("a01b", "a1c") < 0 &&
		natural.Compare("a1b01", "a01b1") < 0 &&
		natural.Compare("x0", "x00") < 0 &&
		natural.Compare("x00", "x000") < 0 &&
		natural.Compare("x000", "x1") < 0)
	check("total-order-exhaustive", totalOrder())
	check("sort-shuffles-50", sortShuffles())
	check("non-ascii", nonASCII())
	check("byte-counter", byteCounter())
	fmt.Printf("total: %d failed\n", failed)
	if failed > 0 {
		os.Exit(1)
	}
}

// totalOrder enumerates all strings over {a,0,1,9} of length 0-4 and
// exhaustively verifies antisymmetry and transitivity over all triples.
func totalOrder() bool {
	ss := []string{""}
	for l := 1; l <= 4; l++ {
		var next []string
		for _, s := range ss {
			if len(s) != l-1 {
				continue
			}
			for _, c := range []string{"a", "0", "1", "9"} {
				next = append(next, s+c)
			}
		}
		ss = append(ss, next...)
	}
	n := len(ss)
	cmp := make([][]int, n)
	for i := range cmp {
		cmp[i] = make([]int, n)
		for j := range cmp[i] {
			cmp[i][j] = natural.Compare(ss[i], ss[j])
		}
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if cmp[i][j] != -cmp[j][i] || (cmp[i][j] == 0) != (i == j) {
				return false
			}
			if cmp[i][j] >= 0 {
				continue
			}
			for k := 0; k < n; k++ {
				if cmp[j][k] < 0 && cmp[i][k] >= 0 {
					return false
				}
			}
		}
	}
	return true
}

// sortShuffles sorts 50 random shuffles of one multiset and requires
// every result to be identical.
func sortShuffles() bool {
	base := []string{"file10", "file2", "a01", "a1", "x0", "x00", "x1",
		"a1b01", "a01b1", "１２", "file2"}
	want := slices.Clone(base)
	natural.Sort(want)
	for seed := int64(0); seed < 50; seed++ {
		s := slices.Clone(base)
		rand.New(rand.NewSource(seed)).Shuffle(len(s), func(i, j int) {
			s[i], s[j] = s[j], s[i]
		})
		natural.Sort(s)
		if !slices.Equal(s, want) {
			return false
		}
	}
	return true
}

// nonASCII ensures multi-byte UTF-8 and full-width digits are treated
// as ordinary non-digit bytes without panicking.
func nonASCII() bool {
	return natural.Compare("文件2", "文件10") < 0 &&
		natural.Compare("１２", "３") < 0 &&
		natural.Compare("é9", "é10") < 0 &&
		natural.Compare("１２", "１２") == 0
}

// byteCounter checks the examined-byte bound on 100k-byte strings that
// differ only in the last byte.
func byteCounter() bool {
	a := strings.Repeat("9", 100000)
	b := strings.Repeat("9", 99999) + "8"
	natural.Compare(a, b)
	return natural.LastCompareBytes() <= 2*(len(a)+len(b))
}
