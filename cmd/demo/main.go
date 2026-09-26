package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"ontology/api"
	"ontology/cmp"
	"ontology/cyc"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK  " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
	}
}

// naive 是 O(n^2) 朴素最小旋转，用作对拍 oracle。
func naive(s string) int {
	best := 0
	for k := 1; k < len(s); k++ {
		if s[k:]+s[:k] < s[best:]+s[:best] {
			best = k
		}
	}
	return best
}

func main() {
	k := cyc.MinRotation("baabaa")
	check(`"baabaa" 最小旋转 "aabaab" 下标 1`, k == 1 && cyc.Rotate("baabaa", k) == "aabaab")
	k = cyc.MinRotation("banana")
	check(`"banana" 最小旋转 "abanan" 下标 5`, k == 5 && cyc.Rotate("banana", k) == "abanan")
	k = cyc.MinRotation("bca")
	check(`"bca" 最小旋转 "abc" 下标 2`, k == 2 && cyc.Rotate("bca", k) == "abc")
	// 题面写 CyclicEqual("aabaa","baaab")=true，但两串 b 的个数为 1 vs 2，
	// 多重集不同，数学上不可能互为旋转（题面笔误），此处按正确语义判定。
	check(`CyclicEqual: ("aabaa","baaab")=false(题面误标true), ("aaabb","baaab")=true`,
		!cmp.CyclicEqual("aabaa", "baaab") && cmp.CyclicEqual("aaabb", "baaab"))

	naiveOK := true
	for _, s := range []string{"baabaa", "banana", "bca", "aaaa", "abab", "z", "aaabb", "zyxwvuts"} {
		if cyc.MinRotation(s) != naive(s) {
			naiveOK = false
		}
	}
	check("内置语料与朴素 O(n^2) 逐串一致", naiveOK)

	st, _ := api.New("baabaa")
	_, eEmpty := api.New("")
	_, eLong := api.New(strings.Repeat("x", 1<<20+1))
	_, eIdx := st.Rotate(6)
	check("三类哨兵错误可判定且互不相同",
		errors.Is(eEmpty, api.ErrEmpty) && errors.Is(eLong, api.ErrTooLong) && errors.Is(eIdx, api.ErrBadIndex) &&
			!errors.Is(eEmpty, api.ErrTooLong) && !errors.Is(eLong, api.ErrBadIndex) && !errors.Is(eIdx, api.ErrEmpty))
	k2, _ := st.MinRotation()
	r2, _ := st.Rotate(k2)
	check("被拒后状态不变、SelfCheck 通过", k2 == 1 && r2 == "aabaab" && st.SelfCheck() == nil)

	const n1, n2 = 100000, 800000
	s1, s2 := strings.Repeat("ab", n1/2)+"c", strings.Repeat("ab", n2/2)+"c"
	t1 := time.Now()
	cyc.MinRotation(s1)
	d1 := time.Since(t1)
	t2 := time.Now()
	cyc.MinRotation(s2)
	d2 := time.Since(t2)
	check("大 n 耗时不随 n^2 增长（8 倍数据耗时 << 64 倍）", d2 < d1*32+50*time.Millisecond)

	const G = 32
	var wg sync.WaitGroup
	oks := make(chan bool, G)
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gk, _ := st.MinRotation()
			gr, _ := st.Rotate(gk)
			oks <- gk == 1 && gr == "aabaab" && st.CyclicEqual("aabaab") && st.SelfCheck() == nil
		}()
	}
	wg.Wait()
	close(oks)
	allSame := true
	for ok := range oks {
		allSame = allSame && ok
	}
	check("32 goroutine 并发只读结果逐项一致", allSame)

	if failed {
		os.Exit(1)
	}
}
