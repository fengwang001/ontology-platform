// demo：快照+增量双读的端到端判定，退出码 0 表示全部通过。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
)

var failed bool

func check(name string, cond bool) {
	if cond {
		fmt.Println("OK  " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
	}
}

func main() {
	// 第三节八步序列
	s := api.New()
	s.Put("a", "1")
	s.Put("b", "2")
	s.Snapshot()
	s.Put("a", "5")
	s.Del("b")
	s.Put("c", "9")
	s.Snapshot()
	s.Put("a", "7")
	v6, ok6, _ := s.Read("a", 6)
	check("Read(a,6)=7", v6 == "7" && ok6)
	_, _, e3 := s.Read("a", 3)
	_, _, e4 := s.Read("a", 4)
	_, _, eb3 := s.Read("b", 3)
	check("Read(a,3)/(a,4)/(b,3) 拒绝(atSeq<snapSeq)",
		errors.Is(e3, api.ErrBeforeSnap) && errors.Is(e4, api.ErrBeforeSnap) && errors.Is(eb3, api.ErrBeforeSnap))
	vc, okc, _ := s.Read("c", 6)
	check("Read(c,6)=9", vc == "9" && okc)
	_, okb6, _ := s.Read("b", 6)
	check("Read(b,6) 不存在", !okb6)

	// 四条不变量（含 Read 与批量重算一致）
	check("SelfCheck 四不变量", s.SelfCheck() == nil)

	// 三类可判定错误互不相同 + 被拒后状态不变
	before, _, _ := s.Read("a", 6)
	e1, e2, e3x := s.Put("", "x"), s.Put("k", ""), s.Del("")
	distinct := errors.Is(e1, api.ErrEmptyKey) && errors.Is(e2, api.ErrEmptyVal) &&
		errors.Is(e3x, api.ErrEmptyKey) && !errors.Is(e1, api.ErrEmptyVal) && !errors.Is(e2, api.ErrEmptyKey)
	after, _, _ := s.Read("a", 6)
	check("三类哨兵错误互不相同", distinct)
	check("被拒后状态不变", before == after && before == "7")

	// 大 m：快照含 m 键后再写 1 键，读快照键结果正确（扫描 O(1) 由 read 包内测试钉住）
	big := api.New()
	const m = 10000
	for i := 0; i < m; i++ {
		big.Put(fmt.Sprintf("k%06d", i), "v")
	}
	big.Snapshot()
	big.Put("k000000", "v2")
	good := true
	for i := 0; i < 100; i++ {
		if v, ok, _ := big.Read("k000042", m+1); v != "v" || !ok {
			good = false
		}
	}
	if v, ok, _ := big.Read("k000000", m+1); v != "v2" || !ok {
		good = false
	}
	check("大 m 读正确(扫描不随 m 增长)", good)

	// 并发只读结果一致，期间另一 goroutine 反复 Snapshot
	const n = 32
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]string, n)
	oks := make([]bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			v, ok, _ := big.Read("k000000", m+1)
			results[i], oks[i] = v, ok
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 50; i++ {
			big.Snapshot()
		}
	}()
	close(start)
	wg.Wait()
	same := true
	for i := range results {
		if results[i] != "v2" || !oks[i] {
			same = false
		}
	}
	check("并发只读结果一致", same)

	if failed {
		os.Exit(1)
	}
}
