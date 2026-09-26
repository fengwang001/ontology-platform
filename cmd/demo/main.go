// Command demo 逐条打印正则匹配器的自检结果，全部 OK 时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"ontology/api"
	"ontology/match"
	"ontology/parse"
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
	check(`("aab","c*a*b")=true`, match.Match("aab", "c*a*b"))
	check(`("a","a.")=false`, !match.Match("a", "a."))
	check(`("mississippi","mis*is*p*.")=false`, !match.Match("mississippi", "mis*is*p*."))
	check(`("ab",".*")=true`, match.Match("ab", ".*"))

	consistent := true
	for _, c := range [][2]string{
		{"aab", "c*a*b"}, {"a", "a."}, {"mississippi", "mis*is*p*."}, {"ab", ".*"},
		{"", ""}, {"", "a*"}, {"", ".*"}, {"", "a*b*"}, {"a", "."}, {"", "."},
		{"aaab", "a*b"}, {"ab", "a.*b"}, {"abc", "a.c"}, {"abc", "a..d"},
	} {
		toks, err := parse.Parse(c[1])
		if err != nil || match.Match(c[0], c[1]) != match.Naive(c[0], toks) {
			consistent = false
		}
	}
	check("与朴素回溯一致", consistent)

	_, e1 := api.Compile("*a")
	_, e2 := api.Compile("a+")
	_, e3 := api.Compile(strings.Repeat("a", parse.MaxLen+1))
	m, _ := api.Compile("a*b")
	_, e4 := m.Match(strings.Repeat("b", parse.MaxLen+1))
	check("三类可判定错误互不相同",
		errors.Is(e1, parse.ErrSyntax) && errors.Is(e2, parse.ErrUnsupported) &&
			errors.Is(e3, parse.ErrTooLong) && errors.Is(e4, parse.ErrTooLong) &&
			!errors.Is(e1, parse.ErrUnsupported) && !errors.Is(e2, parse.ErrSyntax) &&
			!errors.Is(e3, parse.ErrSyntax))

	before, _ := m.Match("aab")
	after, _ := m.Match("aab")
	check("被拒后状态不变", before && after == before)

	// 记忆化 DP 下 m=10000 瞬间完成；朴素回溯在同输入下是指数级。
	start := time.Now()
	big := match.Match(strings.Repeat("a", 10000), "a*a*a*")
	check("大 m 下状态数不随 m 指数增长", big && time.Since(start) < 2*time.Second)

	texts := make([]string, 64)
	serial := make([]bool, 64)
	parallel := make([]bool, 64)
	for i := range texts {
		texts[i] = strings.Repeat(string(rune('a'+i%3)), i)
		serial[i], _ = m.Match(texts[i])
	}
	var wg sync.WaitGroup
	for i := range texts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			parallel[i], _ = m.Match(texts[i])
		}(i)
	}
	wg.Wait()
	same := true
	for i := range serial {
		same = same && serial[i] == parallel[i]
	}
	check("并发结果一致", same)
	check("SelfCheck", api.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
