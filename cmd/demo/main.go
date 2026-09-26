package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"ontology/api"
	"ontology/cmp"
	"ontology/cyc"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK", name)
}

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
	for _, c := range []struct {
		s   string
		k   int
		rot string
	}{
		{"baabaa", 1, "aabaab"},
		{"banana", 5, "abanan"},
		{"bca", 2, "abc"},
	} {
		k := cyc.MinRotation(c.s)
		check(fmt.Sprintf("MinRotation(%q)=%d rot=%q", c.s, c.k, c.rot), k == c.k && cyc.Rotate(c.s, k) == c.rot)
	}
	// 题面称 CyclicEqual("aabaa","baaab") 为 true，但两串字符多重集不同（4a1b vs 3a2b），按给定定义应为 false。
	check(`CyclicEqual("aabaa","baaab")=false (题面"true"与定义矛盾)`, !cmp.CyclicEqual("aabaa", "baaab") && cmp.CyclicEqual("aabaa", "aaaba"))
	naiveOK := true
	for _, s := range []string{"baabaa", "banana", "bca", "aaaa", "abacaba", "zyxwvutsrq"} {
		naiveOK = naiveOK && cyc.MinRotation(s) == naive(s)
	}
	check("MinRotation 与朴素 O(n^2) 逐串一致", naiveOK)
	x, _ := api.New("baabaa")
	e1, e2 := error(nil), error(nil)
	_, e1 = api.New("")
	_, e2 = api.New(string(make([]byte, 1<<21)))
	_, e3 := x.Rotate(6)
	_, e4 := x.Rotate(-1)
	check("三类哨兵错误互不相同", errors.Is(e1, api.ErrEmpty) && errors.Is(e2, api.ErrTooLong) &&
		errors.Is(e3, api.ErrIndex) && errors.Is(e4, api.ErrIndex) &&
		e1 != e2 && e2 != e3 && e1 != e3)
	k0, _ := x.MinRotation()
	r0, _ := x.Rotate(k0)
	k1, _ := x.MinRotation()
	r1, _ := x.Rotate(k1)
	check("被拒后状态不变且仍可用", k0 == k1 && r0 == r1 && k0 == 1 && r0 == "aabaab")
	check("SelfCheck 四条不变量", x.SelfCheck())
	const N = 32
	ks, rs := make([]int, N), make([]string, N)
	var wg sync.WaitGroup
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			ks[g], _ = x.MinRotation()
			rs[g], _ = x.Rotate(ks[g])
		}(g)
	}
	wg.Wait()
	same := true
	for g := 1; g < N; g++ {
		same = same && ks[g] == ks[0] && rs[g] == rs[0]
	}
	check("并发只读结果逐项相同", same)
	big := "b" + strings.Repeat("a", 99999)
	kb := cyc.MinRotation(big)
	check("大 n 结果正确；比较次数线性由 cyc 包测试钉住", kb == 1 && cyc.Rotate(big, kb) == strings.Repeat("a", 99999)+"b")
	if failed {
		os.Exit(1)
	}
}
