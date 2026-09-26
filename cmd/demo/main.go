package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"ontology/api"
	"ontology/pal"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	e, err := api.New("aabaa")
	check("构建 aabaa", err == nil)
	check("不同回文数=5", e.DistinctPalindromes() == 5)
	check("总回文数=9", e.TotalPalindromes() == 9)
	n, err := e.Count("a")
	check(`Count("a")=4`, err == nil && n == 4)
	check(`最长回文="aabaa"(长5)`, e.LongestPalindrome() == "aabaa")
	check("与朴素一致(SelfCheck)", api.SelfCheck() == nil)

	_, errEmpty := api.New("")
	_, errLong := api.New(strings.Repeat("a", pal.MaxLen+1))
	_, errNP := e.Count("ab")
	check("三类错误可判定且互异", errors.Is(errEmpty, api.ErrEmpty) &&
		errors.Is(errLong, api.ErrTooLong) &&
		errors.Is(errNP, api.ErrNotPalindrome) &&
		errEmpty != errLong && errLong != errNP && errEmpty != errNP)

	d, t, l := e.DistinctPalindromes(), e.TotalPalindromes(), e.LongestPalindrome()
	check("被拒后状态不变", d == 5 && t == 9 && l == "aabaa")

	big, _ := api.New(stress(10000))
	check("大n节点数<=n+2", big.DistinctPalindromes()+2 <= 10000+2)

	want := [4]string{fmt.Sprint(d), fmt.Sprint(t), l, fmt.Sprint(must(e.Count("a")))}
	var wg sync.WaitGroup
	bad := make(chan bool, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got := [4]string{fmt.Sprint(e.DistinctPalindromes()), fmt.Sprint(e.TotalPalindromes()),
				e.LongestPalindrome(), fmt.Sprint(must(e.Count("a")))}
			if got != want || api.SelfCheck() != nil {
				bad <- true
			}
		}()
	}
	wg.Wait()
	check("并发只读结果一致", len(bad) == 0)

	if failed {
		os.Exit(1)
	}
}

// stress 生成确定性的 n 长小字母表串。
func stress(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteByte(byte('a' + (i*7+i/13)%3))
	}
	return b.String()
}

func must(n int, err error) int {
	if err != nil {
		failed = true
	}
	return n
}
