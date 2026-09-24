// demo 顺序演示：八步推导、变更日志自洽、三类错误、失败不留痕、
// 大 m 撤回、并发只读。全部通过则退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"

	"ontology/api"
	"ontology/union"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Println(map[bool]string{true: "OK  ", false: "FAIL"}[ok] + name)
}

func iv(s, e int64) api.Interval { return api.Interval{S: s, E: e} }
func ch(d bool, s, e int64) api.Change {
	return api.Change{Del: d, S: s, E: e}
}

func main() {
	// 第三节八步：每步并集分段 + 第 2/4/5 步变更日志判定。
	a := api.New(8)
	steps := []struct {
		add  bool
		s, e int64
		view []api.Interval
	}{
		{true, 0, 10, []api.Interval{iv(0, 10)}},
		{true, 10, 20, []api.Interval{iv(0, 20)}},
		{true, 30, 40, []api.Interval{iv(0, 20), iv(30, 40)}},
		{false, 15, 35, []api.Interval{iv(0, 15), iv(35, 40)}},
		{true, 15, 35, []api.Interval{iv(0, 40)}},
		{false, 20, 25, []api.Interval{iv(0, 20), iv(25, 40)}},
		{true, 20, 25, []api.Interval{iv(0, 40)}},
		{false, 0, 40, nil},
	}
	var logs []api.Change
	var stepLog [8][]api.Change
	ok := true
	for i, st := range steps {
		var c []api.Change
		var err error
		if st.add {
			c, err = a.Add(st.s, st.e)
		} else {
			c, err = a.Withdraw(st.s, st.e)
		}
		stepLog[i] = c
		logs = append(logs, c...)
		ok = ok && err == nil && slices.Equal(a.View(), st.view)
	}
	check("eight-step trace: view after each step", ok)
	check("step2/4/5 changelogs (adjacent merge, empty-residual, multi-span)",
		slices.Equal(stepLog[1], []api.Change{ch(true, 0, 10), ch(false, 0, 20)}) &&
			slices.Equal(stepLog[3], []api.Change{ch(true, 0, 20), ch(false, 0, 15), ch(true, 30, 40), ch(false, 35, 40)}) &&
			slices.Equal(stepLog[4], []api.Change{ch(true, 0, 15), ch(true, 35, 40), ch(false, 0, 40)}))
	// 变更日志每个前缀自洽：逐条应用到影子并集。
	var shadow []api.Interval
	ok = true
	for _, c := range logs {
		if shadow, ok = union.ApplyChange(shadow, c); !ok {
			break
		}
	}
	check("changelog prefixes self-consistent", ok && len(shadow) == 0)
	// 三类可判定错误互不相同；被拒后状态不变且仍可用。
	b := api.New(2)
	b.Add(0, 5)
	b.Add(10, 15)
	before := b.View()
	_, e1 := b.Add(3, 3)
	_, e2 := b.Add(20, 25)
	_, e3 := b.Withdraw(6, 8)
	check("three distinct sentinel errors; rejection leaves no trace",
		errors.Is(e1, api.ErrInvalidInterval) && errors.Is(e2, api.ErrTooManyIntervals) &&
			errors.Is(e3, api.ErrNotFound) && !errors.Is(e1, e2) && !errors.Is(e2, e3) &&
			slices.Equal(b.View(), before))
	// 大 m：检查个数不随 m 增长（由 union 包内测试钉住，此处验证正确性）。
	c := api.New(10001)
	for i := int64(0); i < 10000; i++ {
		c.Add(10*i, 10*i+5)
	}
	c.Withdraw(50002, 50003)
	check("large-m withdraw correct (scan bound pinned by union test)",
		slices.Contains(c.View(), iv(50000, 50002)) && slices.Contains(c.View(), iv(50003, 50005)))
	// 并发只读：N 个 goroutine 同时 View/SelfCheck，结果逐段相同。
	want := c.View()
	var wg sync.WaitGroup
	ok = true
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !slices.Equal(c.View(), want) || c.SelfCheck() != nil {
				ok = false
			}
		}()
	}
	wg.Wait()
	check("concurrent read-only views identical", ok)
	check("SelfCheck passes all four invariants", api.New(8).SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
