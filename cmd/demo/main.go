// demo 逐条核验迟到纠正重处理的各项判定，全部 OK 退出码 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"

	"ontology/api"
)

var fails int

func ok(name string, cond bool) {
	if cond {
		fmt.Println("OK   " + name)
	} else {
		fmt.Println("FAIL " + name)
		fails++
	}
}

func main() {
	e, _ := api.New(3)
	type ev struct{ s, v int64 }
	events := []ev{{5, 10}, {7, 20}, {6, 15}, {8, 5}, {5, 99}, {6, 15}, {6, 30}, {9, 3}}
	wantSum := []int64{10, 30, 45, 50, 50, 50, 65, 68}
	sts := make([]api.Status, 8)
	sums := make([]int64, 8)
	var all []api.Change
	good := true
	for i, v := range events {
		cs, st, err := e.Apply("k", v.s, v.v)
		sts[i], sums[i] = st, e.View()["k"]
		all = append(all, cs...)
		good = good && err == nil && sums[i] == wantSum[i]
	}
	ok(fmt.Sprintf("八步 sum=%v 变更日志前缀自洽", sums), good && prefixOK(all))
	ok("第5步 (5,99) 过期拒绝 sum=50 stale=1", sts[4] == api.StatusStale && sums[4] == 50 && e.Stale() == 1)
	ok("第6步 (6,15) 重复幂等 sum=50 无产出", sts[5] == api.StatusDuplicate && sums[5] == 50)
	ok("第3步 (6,15) 迟到新 Seq 接受 sum=45", sts[2] == api.StatusLate && sums[2] == 45)

	e2, _ := api.New(2)
	_, _, errEmpty := e2.Apply("", 1, 1)
	_, _, errSeq := e2.Apply("x", 0, 1)
	_, errW := api.New(0)
	ok("三类错误可判定且互不相同", errors.Is(errEmpty, api.ErrEmptyKey) &&
		errors.Is(errSeq, api.ErrBadSeq) && errors.Is(errW, api.ErrBadWindow) &&
		!errors.Is(errEmpty, api.ErrBadSeq) && !errors.Is(errSeq, api.ErrEmptyKey) &&
		!errors.Is(errW, api.ErrEmptyKey))
	v0, s0 := e.View(), e.Stale()
	e.Apply("", 1, 1)
	e.Apply("k", -1, 5)
	ok("被拒后状态不变且可继续用", e.Stale() == s0 && e.View()["k"] == v0["k"] && len(e.View()) == len(v0))
	ok("SelfCheck 四条不变量", e.SelfCheck() == nil)

	err := exec.Command("go", "test", "-count=1", "-run", "TestWindowChecksO1", "./agg/").Run()
	ok("大 m 窗口成员判定 O(1)（go test ./agg）", err == nil)

	ec, _ := api.New(64)
	const n = 64
	var wg sync.WaitGroup
	for i := 1; i <= n; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); ec.Apply("c", int64(i), int64(i)) }(i)
	}
	wg.Wait()
	ok(fmt.Sprintf("并发 Apply 和=%d", ec.View()["c"]), ec.View()["c"] == n*(n+1)/2)

	if fails > 0 {
		os.Exit(1)
	}
}

// prefixOK 重放变更日志每个前缀，校验撤回一致性。
func prefixOK(cs []api.Change) bool {
	cur := map[string]int64{}
	for _, c := range cs {
		v, has := cur[c.Key]
		if c.Plus {
			if has {
				return false
			}
			cur[c.Key] = c.Sum
		} else {
			if !has || v != c.Sum {
				return false
			}
			delete(cur, c.Key)
		}
	}
	return true
}
