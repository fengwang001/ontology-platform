// demo：逐条打印回文树各项判定，全部 OK 时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"ontology/api"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Println(map[bool]string{true: "OK  ", false: "FAIL "}[ok] + name)
}

func main() {
	h, err := api.New("aabaa")
	check("不同回文数=5", err == nil && h.DistinctPalindromes() == 5)
	check("总回文数=9", h.TotalPalindromes() == 9)
	ca, _ := h.Count("a")
	check("Count(a)=4", ca == 4)
	check("最长回文=aabaa(长5)", h.LongestPalindrome() == "aabaa")
	check("SelfCheck 与朴素一致/四条不变量", h.SelfCheck() == nil)

	_, e1 := api.New("")
	_, e2 := api.New(strings.Repeat("a", api.MaxLen+1))
	_, e3 := h.Count("ab")
	check("三类可判定错误互不相同", errors.Is(e1, api.ErrEmpty) && errors.Is(e2, api.ErrTooLong) &&
		errors.Is(e3, api.ErrNotPalindrome) && !errors.Is(e1, e2) && !errors.Is(e2, e3) && !errors.Is(e1, e3))
	check("被拒后状态不变", h.DistinctPalindromes() == 5 && h.TotalPalindromes() == 9 && h.LongestPalindrome() == "aabaa")

	big, seed := make([]byte, 10000), uint32(12345)
	for i := range big { // LCG 生成确定性伪随机串
		seed = seed*1664525 + 1013904223
		big[i] = 'a' + byte(seed>>24)%26
	}
	hb, err := api.New(string(big))
	check("大n=10000 节点数<=n+2", err == nil && hb.DistinctPalindromes()+2 <= len(big)+2)

	const n = 8
	type res struct {
		d, t, c int
		l       string
	}
	want := res{h.DistinctPalindromes(), h.TotalPalindromes(), ca, h.LongestPalindrome()}
	gots := make([]res, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			<-start
			c, _ := h.Count("a")
			_ = h.SelfCheck()
			gots[k] = res{h.DistinctPalindromes(), h.TotalPalindromes(), c, h.LongestPalindrome()}
		}(i)
	}
	close(start)
	wg.Wait()
	same := true
	for _, g := range gots {
		same = same && g == want
	}
	check("并发只读结果一致", same)

	if failed {
		fmt.Println("FAIL overall")
		os.Exit(1)
	}
	fmt.Println("OK  overall")
}
