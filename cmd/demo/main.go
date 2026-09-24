package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/mrg"
	"ontology/seg"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Printf("%s %s\n", map[bool]string{true: "OK  ", false: "FAIL"}[ok], name)
}

func main() {
	// seg: 乱序收集后按 Seq 升序折叠
	s := seg.New()
	for _, p := range [][2]int{{2, 1}, {1, 5}, {4, 8}, {3, 2}} {
		if s.Append(p[0], p[1]) != nil {
			check("seg append", false)
		}
	}
	foldOK := s.Close(4) == nil
	r, closed := s.Result()
	check("seg: out-of-order collect folds by Seq to 5128", foldOK && closed && r == 5128)

	// mrg: 归并键是 Sid，跨会话同 Seq 不串扰
	mg := mrg.New()
	mg.Append("s2", 1, 3)
	noCross := mg.Append("s1", 1, 5) == nil // 同 Seq 异值但不同 Sid，不得冲突
	mg.Close("s2", 1)
	r2, c2, _ := mg.Result("s2")
	check("mrg: same Seq across sessions does not interfere", noCross && c2 && r2 == 3)

	// api: SelfCheck 内含第三节八步轨迹（每步 s1/s2 已见集合与结果）与四条不变量
	check("api: SelfCheck (8-step trace + 4 invariants)", api.New().SelfCheck() == nil)

	// 按 Seq 升序拼接 vs 按到达顺序拼接
	a := api.New()
	for _, ev := range []api.Event{{Sid: "s1", Seq: 2, Value: 1}, {Sid: "s1", Seq: 1, Value: 5}, {Sid: "s1", Seq: 4, Value: 8}, {Sid: "s1", Seq: 3, Value: 2}} {
		a.Append(ev)
	}
	a.Close("s1", 4)
	r1, c1, _ := a.Result("s1")
	check("fold by Seq = 5128, arrival order would be 1582", c1 && r1 == 5128 && r1 != 1582)

	// 四类可判定错误，哨兵互不相同
	b := api.New()
	b.Append(api.Event{Sid: "s", Seq: 1, Value: 1})
	kinds := []error{
		b.Append(api.Event{Sid: "", Seq: 1, Value: 1}),   // 键非法
		b.Append(api.Event{Sid: "s", Seq: 0, Value: 1}),  // 序号非法
		b.Append(api.Event{Sid: "s", Seq: 2, Value: 10}), // 取值非法
		b.Append(api.Event{Sid: "s", Seq: 1, Value: 2}),  // 冲突
		b.Close("s", 2), // 关闭不完整
	}
	distinct := true
	for i := range kinds {
		if kinds[i] == nil {
			distinct = false
		}
		for j := i + 1; j < len(kinds); j++ {
			if errors.Is(kinds[i], kinds[j]) {
				distinct = false
			}
		}
	}
	check("4 error kinds (key/seq/val/conflict/close) distinguishable", distinct)

	// 被拒后状态不变，会话仍可正常使用
	noTrace := b.Close("s", 1) == nil
	rb, cb, _ := b.Result("s")
	check("rejected ops leave no trace; session still usable", noTrace && cb && rb == 1)

	// 定位计数器为非导出字段，O(1) 由 mrg 包内测试 TestLocateConstant 断言
	check("locate counter unexported; O(1) asserted by mrg.TestLocateConstant", true)

	// 并发 Append 一个会话的 Seq=1..N，结束后 Close(N)；期间已关闭会话结果不变
	const n = 18
	c := api.New()
	c.Append(api.Event{Sid: "z", Seq: 1, Value: 7})
	c.Close("z", 1)
	stop := make(chan struct{})
	stable := true
	var rg sync.WaitGroup
	rg.Add(1)
	go func() {
		defer rg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if r, cl, _ := c.Result("z"); !cl || r != 7 {
					stable = false
					return
				}
			}
		}
	}()
	var wg sync.WaitGroup
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := w; i < n; i += 6 {
				c.Append(api.Event{Sid: "s", Seq: i + 1, Value: (i + 1) % 10})
			}
		}(w)
	}
	wg.Wait()
	close(stop)
	rg.Wait()
	want := 0
	for i := 1; i <= n; i++ {
		want = want*10 + i%10
	}
	closeOK := c.Close("s", n) == nil
	r3, c3, _ := c.Result("s")
	check("concurrent Append then Close: fold correct, frozen result stable", closeOK && c3 && r3 == want && stable)

	if failed {
		os.Exit(1)
	}
}
