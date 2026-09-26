// 后缀自动机演示程序：逐条打印 OK/FAIL，全部通过时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/query"
	"ontology/sam"
)

var bad = false

func check(name string, ok bool) {
	if !ok {
		bad = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK  ", name)
}

func main() {
	m, err := api.New("abcbc")
	if err != nil {
		fmt.Println("FAIL New:", err)
		os.Exit(1)
	}
	check(`不同子串数 "abcbc"=12`, m.DistinctSubstrings() == 12)
	ob, _ := m.Occurrences("b")
	obc, _ := m.Occurrences("bc")
	check(`Occurrences("b")=2 且 ("bc")=2`, ob == 2 && obc == 2)
	lcs, _ := m.LongestCommonSubstring("bcabc")
	check(`LongestCommonSubstring("bcabc")="abc"`, lcs == "abc")
	check(`query 包直查 Distinct=12`, query.New(sam.Build("abcbc")).Distinct() == 12)
	check("SelfCheck：与朴素一致等四条不变量", m.SelfCheck() == nil)
	_, e1 := api.New("")
	_, e2 := api.New(strings.Repeat("x", 1<<20+1)) // 1<<20 为 api 的串长上限
	_, e3 := m.Occurrences("")
	_, e4 := m.LongestCommonSubstring("")
	check("三类可判定错误互不相同", errors.Is(e1, api.ErrEmptyString) &&
		errors.Is(e2, api.ErrTooLong) && errors.Is(e3, api.ErrEmptyQuery) &&
		errors.Is(e4, api.ErrEmptyQuery) && e1 != e2 && e2 != e3 && e1 != e3)
	ob2, _ := m.Occurrences("b")
	check("被拒后状态不变", m.DistinctSubstrings() == 12 && ob2 == 2)
	ok := true
	r := rand.New(rand.NewSource(1))
	for _, n := range []int{100, 1000, 10000} {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte('a' + r.Intn(26))
		}
		ok = ok && sam.LinearBoundOK(string(b))
	}
	check("大 n 状态总数不超过 2n", ok)
	check("并发只读结果一致", concurrentOK(m))
	if bad {
		os.Exit(1)
	}
}

func concurrentOK(m *api.SAM) bool {
	wantD := m.DistinctSubstrings()
	wantO, _ := m.Occurrences("bc")
	wantL, _ := m.LongestCommonSubstring("bcabc")
	start := make(chan struct{})
	var same atomic.Bool
	same.Store(true)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 100; i++ {
				o, _ := m.Occurrences("bc")
				l, _ := m.LongestCommonSubstring("bcabc")
				if m.DistinctSubstrings() != wantD || o != wantO || l != wantL {
					same.Store(false)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	return same.Load()
}
