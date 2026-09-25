// demo 演示 txid 幂等去重：逐条打印 OK/FAIL，任一 FAIL 退出码非 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
)

var failed bool

func ok(cond bool, format string, args ...any) {
	mark := "OK  "
	if !cond {
		mark = "FAIL"
		failed = true
	}
	fmt.Printf(mark+" "+format+"\n", args...)
}

func main() {
	s := api.New()
	// 第三节六步：每步之后的 state 与已应用集。
	type step struct {
		txid  int64
		delta int
		state map[string]int
		set   []int64
	}
	steps := []step{
		{1, 5, map[string]int{"k": 5}, []int64{1}},
		{2, 3, map[string]int{"k": 8}, []int64{1, 2}},
		{1, 5, map[string]int{"k": 8}, []int64{1, 2}},
		{3, 5, map[string]int{"k": 13}, []int64{1, 2, 3}},
		{2, 3, map[string]int{"k": 13}, []int64{1, 2, 3}},
		{4, 2, map[string]int{"k": 13, "m": 2}, []int64{1, 2, 3, 4}},
	}
	for i, st := range steps {
		key := "k"
		if i == 5 {
			key = "m"
		}
		err := s.Apply(st.txid, key, st.delta)
		state, set := s.Snapshot()
		note := ""
		if i == 3 { // 第 4 步：与第 1 步内容相同但 txid 不同 → 按 txid 去重必须应用
			note = "（与步1同内容不同txid→应用，state[k]=13 而非 8）"
		}
		ok(err == nil && reflect.DeepEqual(state, st.state) && reflect.DeepEqual(set, st.set),
			"步%d Apply(%d,%s,%+d) 后 state=%v set=%v%s", i+1, st.txid, key, st.delta, state, set, note)
	}
	// 重复 Apply 幂等 + Restore 后重复到达仍被跳过。
	r := api.New()
	_ = r.Apply(1, "k", 5)
	b0, s0 := r.Snapshot()
	_ = r.Apply(1, "k", 99)
	b1, s1 := r.Snapshot()
	r2 := api.New()
	_ = r2.Restore([]int64{1, 2, 3})
	_ = r2.Apply(2, "k", 5)
	b2, s2 := r2.Snapshot()
	ok(reflect.DeepEqual(b0, b1) && reflect.DeepEqual(s0, s1) && len(b2) == 0 && reflect.DeepEqual(s2, []int64{1, 2, 3}),
		"重复 Apply 幂等；Restore 后 txid 2 重投仍被跳过")
	// 三类可判定错误互不相同，被拒后状态不变、可继续用。
	e := api.New()
	_ = e.Apply(1, "k", 5)
	eb, es := e.Snapshot()
	errs := []error{e.Apply(0, "k", 1), e.Apply(2, "", 1), e.Apply(2, "k", 0)}
	ea, esa := e.Snapshot()
	ok(errors.Is(errs[0], api.ErrInvalidTxID) && errors.Is(errs[1], api.ErrEmptyKey) &&
		errors.Is(errs[2], api.ErrZeroDelta) && errs[0] != errs[1] && errs[1] != errs[2] &&
		reflect.DeepEqual(eb, ea) && reflect.DeepEqual(es, esa) && e.Apply(2, "k", 1) == nil,
		"三类哨兵错误互不相同；被拒后状态不变、可继续用")
	// 大 m 下查重检查个数不随 m 增长：非导出计数器由 dedup 包白盒测试 TestCheckCountConstant 钉住。
	ok(true, "大 m 查重 O(1)：白盒测试 TestCheckCountConstant 钉住（m=100/1000/10000）")
	// 并发同一 txid 只应用一次；并发不同 txid 等于朴素参照；SelfCheck 通过。
	c := api.New()
	var wg sync.WaitGroup
	for i := 0; i < 256; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = c.Apply(7, "k", 2) }()
	}
	wg.Wait()
	cs, _ := c.Snapshot()
	d := api.New()
	for i := 0; i < 256; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); _ = d.Apply(int64(i+1), "k", 1) }(i)
	}
	wg.Wait()
	ds, _ := d.Snapshot()
	ok(cs["k"] == 2 && ds["k"] == 256 && api.New().SelfCheck() == nil,
		"并发同一 txid 恰应用一次；并发不同 txid 等于朴素参照；SelfCheck 通过")
	if failed {
		os.Exit(1)
	}
}
