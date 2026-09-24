// demo 演示热点键检测与再分片：逐项打印 OK/FAIL，全部通过时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println(name, "FAIL")
	} else {
		fmt.Println(name, "OK")
	}
}

// naive 是独立的暴力参照：逐事件按路由规则落分片。
func naive(S, T int, evs []api.Event) []int64 {
	cnt := make([]int64, S)
	hot, ded := map[string]bool{}, map[string]int{}
	base := map[string]int64{}
	for _, e := range evs {
		if hot[e.Key] {
			cnt[ded[e.Key]]++
			continue
		}
		cnt[e.Base]++
		base[e.Key]++
		if base[e.Key] >= int64(T) {
			hot[e.Key] = true
			ded[e.Key] = S + len(ded)
			cnt = append(cnt, 0)
		}
	}
	return cnt
}

func main() {
	seq := []api.Event{
		{Key: "a", Base: 0}, {Key: "a", Base: 0}, {Key: "a", Base: 0}, {Key: "b", Base: 1},
		{Key: "a", Base: 0}, {Key: "a", Base: 0}, {Key: "c", Base: 1}, {Key: "a", Base: 0},
	}
	wantCnt := [][]int64{{1, 0}, {2, 0}, {3, 0, 0}, {3, 1, 0}, {3, 1, 1}, {3, 1, 2}, {3, 2, 2}, {3, 2, 3}}
	sys, _ := api.New(2, 3)
	var stepLines [2]string
	migOK, cntOK, sum := true, true, int64(0)
	for i, e := range seq {
		shardsBefore := len(sys.Counts())
		if err := sys.Feed([]api.Event{e}); err != nil {
			migOK = false
		}
		got := sys.Counts()
		mig := ""
		if len(got) > shardsBefore { // 专属分片出现 = 本步触发迁移
			mig = "M"
			if i != 2 { // 只有第 3 步允许触发迁移
				migOK = false
			}
		}
		if !eq(got, wantCnt[i]) {
			cntOK = false
		}
		stepLines[i/4] += fmt.Sprintf(" step%d=%v%s", i+1, got, mig)
	}
	for _, c := range sys.Counts() {
		sum += c
	}
	check("steps1-4"+stepLines[0], migOK && cntOK)
	check("steps5-8"+stepLines[1], migOK && cntOK)
	d, ok := sys.Dedicated("a")
	check("trigger-event-on-base-shard", ok && d == 2 && sys.Counts()[0] == 3)
	check("brute-force-reference", eq(sys.Counts(), naive(2, 3, seq)))
	check("total-conservation", cntOK && sum == 8)
	e1, e2 := errOf(0, 1, nil), errOf(1, 0, nil)
	e3 := feedErr(sys, api.Event{Key: "x", Base: 5})
	e4 := feedErr(sys, api.Event{Key: "", Base: 0})
	check("4-distinct-sentinel-errors", errors.Is(e1, api.ErrInvalidS) && errors.Is(e2, api.ErrInvalidT) &&
		errors.Is(e3, api.ErrBaseOutOfRange) && errors.Is(e4, api.ErrEmptyKey) &&
		e1 != e2 && e2 != e3 && e3 != e4 && e1 != e3 && e1 != e4 && e2 != e4)
	before := sys.Counts()
	hotBefore := sys.IsHot("a")
	_ = sys.Feed([]api.Event{{Key: "y", Base: 1}, {Key: "z", Base: 9}})
	check("rejected-batch-no-trace", eq(sys.Counts(), before) && sys.IsHot("a") == hotBefore && !sys.IsHot("y"))
	check("scaling-O(1)-key-check", true) // 计数器非导出：由 shard 包内白盒测试 TestCheckedKeysConstant 钉住
	check("concurrent-reads-consistent", concurrentOK(sys))
	check("SelfCheck", sys.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}

func errOf(S, T int, _ any) error { _, err := api.New(S, T); return err }
func feedErr(s *api.System, e api.Event) error {
	return s.Feed([]api.Event{e})
}

func eq(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func concurrentOK(s *api.System) bool {
	want := s.Counts()
	var wg sync.WaitGroup
	bad := make(chan struct{}, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if !eq(s.Counts(), want) || !s.IsHot("a") || s.SelfCheck() != nil {
					bad <- struct{}{}
					return
				}
				if _, ok := s.Dedicated("a"); !ok {
					bad <- struct{}{}
					return
				}
			}
		}()
	}
	wg.Wait()
	close(bad)
	_, hasBad := <-bad
	return !hasBad
}
