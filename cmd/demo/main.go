package main

import (
	"fmt"
	"math/rand"
	"os"
	"strings"

	"ontology/natural"
)

var failures int

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", status, name)
}

func gen(alphabet string, maxLen int) []string {
	out := []string{""}
	for n := 1; n <= maxLen; n++ {
		var rec func(prefix string)
		rec = func(prefix string) {
			if len(prefix) == n {
				out = append(out, prefix)
				return
			}
			for i := 0; i < len(alphabet); i++ {
				rec(prefix + alphabet[i:i+1])
			}
		}
		rec("")
	}
	return out
}

func totalOrderOK(ss []string) bool {
	n := len(ss)
	cmp := make([][]int, n)
	for i := range cmp {
		cmp[i] = make([]int, n)
		for j := range cmp[i] {
			cmp[i][j] = natural.Compare(ss[i], ss[j])
			if cmp[i][j] != -natural.Compare(ss[j], ss[i]) {
				return false
			}
			if (cmp[i][j] == 0) != (ss[i] == ss[j]) {
				return false
			}
		}
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			for k := 0; k < n; k++ {
				if cmp[i][j] < 0 && cmp[j][k] < 0 && cmp[i][k] >= 0 {
					return false
				}
			}
		}
	}
	return true
}

func shufflesOK(ss []string) bool {
	want := append([]string(nil), ss...)
	natural.Sort(want)
	r := rand.New(rand.NewSource(1))
	for trial := 0; trial < 50; trial++ {
		got := append([]string(nil), ss...)
		r.Shuffle(len(got), func(i, j int) { got[i], got[j] = got[j], got[i] })
		natural.Sort(got)
		if strings.Join(got, "") != strings.Join(want, "") {
			return false
		}
	}
	return true
}

func main() {
	check("basic", natural.Less("file2", "file10") &&
		natural.Less("a9b", "a10a") && natural.Less("x", "x1"))
	big1 := "n" + strings.Repeat("9", 39) + "8"
	big2 := "n" + strings.Repeat("9", 39) + "9"
	check("big40digit", natural.Less(big1, big2))
	check("leadingZeros", natural.Less("a1", "a01") && natural.Less("a01", "a001") &&
		natural.Less("a01b", "a1c") && natural.Less("a1b01", "a01b1") &&
		natural.Less("x0", "x00") && natural.Less("x00", "x000") && natural.Less("x000", "x1"))
	check("totalOrder", totalOrderOK(gen("a019", 3)))
	check("shuffle50", shufflesOK(gen("a01", 3)))
	check("nonASCII", natural.Less("文件2", "文件10") &&
		natural.Compare("１２", "１２") == 0 && natural.Less("１２", "１３"))
	a := strings.Repeat("x", 99999) + "a"
	b := strings.Repeat("x", 99999) + "b"
	natural.Compare(a, b)
	check("byteCounter", natural.LastCompareBytes() <= int64(2*(len(a)+len(b))))
	fmt.Printf("total: %d failure(s)\n", failures)
	if failures > 0 {
		os.Exit(1)
	}
}
