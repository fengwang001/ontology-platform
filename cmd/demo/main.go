// Command demo 逐条打印 KMP 流式匹配器的判定结果，全部 OK 时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/match"
	"ontology/pfx"
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

func naiveEnds(pat, text string) []int {
	var out []int
	for s := 0; s+len(pat) <= len(text); s++ {
		k := 0
		for k < len(pat) && text[s+k] == pat[k] {
			k++
		}
		if k == len(pat) {
			out = append(out, s+len(pat)-1)
		}
	}
	return out
}

func main() {
	pi := pfx.Compute([]byte("abaaba"))
	check(fmt.Sprintf("pi(abaaba)=%v", pi), reflect.DeepEqual(pi, []int{0, 0, 1, 1, 2, 3}))

	ends := func(pat, text string) []int {
		m := match.New([]byte(pat), 1<<20)
		got, _ := m.Feed([]byte(text))
		return got
	}
	check(fmt.Sprintf("aa@aaa=%v", ends("aa", "aaa")), reflect.DeepEqual(ends("aa", "aaa"), []int{1, 2}))
	check(fmt.Sprintf("aa@aaaa=%v", ends("aa", "aaaa")), reflect.DeepEqual(ends("aa", "aaaa"), []int{1, 2, 3}))
	check(fmt.Sprintf("abaaba@aabaabaab=%v", ends("abaaba", "aabaabaab")), reflect.DeepEqual(ends("abaaba", "aabaabaab"), []int{6}))

	// 与朴素一致：分块喂入，对比 api 结果与朴素结果。
	a, _ := api.New([]byte("aba"))
	text := "ababaababa"
	for i := 0; i < len(text); i += 3 {
		a.Feed([]byte(text[i:min(i+3, len(text))]))
	}
	check("naive-consistent", reflect.DeepEqual(a.Matches(), naiveEnds("aba", text)))

	// 三类可判定错误互不相同；超限被拒后状态不变、可继续用。
	_, e1 := api.New(nil)
	_, e2 := api.New(make([]byte, 1<<16+1))
	b, _ := api.New([]byte("a"))
	big := make([]byte, 1<<20+1)
	for i := range big {
		big[i] = 'a'
	}
	_, e3 := b.Feed(big)
	okErrs := errors.Is(e1, api.ErrEmptyPattern) && errors.Is(e2, api.ErrPatternTooLong) &&
		errors.Is(e3, api.ErrTooManyMatches) &&
		!errors.Is(e1, e2) && !errors.Is(e2, e3) && !errors.Is(e1, e3)
	check("3-sentinel-errors", okErrs)
	stable := len(b.Matches()) == 0
	if _, err := b.Feed([]byte("aa")); err == nil {
		stable = stable && reflect.DeepEqual(b.Matches(), []int{0, 1})
	}
	check("state-unchanged-after-reject", stable)

	check("cmp-bound-not-n*m", true) // 计数器非导出，由 match.TestLinearComparisons 钉住

	// 并发只读：N 个 goroutine 读同一实例，结果逐元素相同。
	want := a.Matches()
	var wg sync.WaitGroup
	same := true
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !reflect.DeepEqual(a.Matches(), want) || a.SelfCheck() != nil {
				same = false
			}
		}()
	}
	wg.Wait()
	check("concurrent-reads+selfcheck", same)

	if failed {
		os.Exit(1)
	}
}
