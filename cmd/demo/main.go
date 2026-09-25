package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/pool"
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

func main() {
	// 1. 第三节六步：{a:1,b:2,d:1}，驱逐 c。
	api.New(3)
	for _, s := range []string{"a", "b", "c", "b"} {
		api.Intern(s)
	}
	api.Release("c")
	api.Intern("d")
	snap := api.Snapshot()
	check("六步后 {a:1,b:2,d:1} 且驱逐 c", len(snap) == 3 &&
		snap[0] == api.Entry{Value: "d", Refs: 1} &&
		snap[1] == api.Entry{Value: "b", Refs: 2} &&
		snap[2] == api.Entry{Value: "a", Refs: 1})
	// 2. 与朴素重放一致（SelfCheck 内置 3000 步随机序列比对）。
	check("与朴素重放一致", api.SelfCheck() == nil)
	// 3. 驻留唯一：返回值等于输入，Snapshot 无重复值，Len == 去重数。
	v, _ := api.Intern("a")
	seen := map[string]bool{}
	dup := false
	for _, e := range api.Snapshot() {
		dup = dup || seen[e.Value]
		seen[e.Value] = true
	}
	check("驻留唯一", v == "a" && !dup && api.Len() == len(seen) && api.Len() <= 3)
	// 4. 计数>0 不被驱逐。
	api.New(2)
	api.Intern("x")
	api.Intern("y")
	_, errFull := api.Intern("z")
	api.Release("x")
	api.Intern("z")
	snap = api.Snapshot()
	check("计数>0 不被驱逐", errors.Is(errFull, api.ErrFull) && len(snap) == 2 &&
		snap[0].Value == "z" && snap[1].Value == "y")
	// 5. 四类可判定错误互不相同。
	errs := []error{api.New(0), errFull, api.Release("nope"), func() error {
		api.New(1)
		api.Intern("s")
		api.Release("s")
		return api.Release("s")
	}()}
	want := []error{api.ErrInvalidMaxEntries, api.ErrFull, api.ErrNotInterned, api.ErrDoubleRelease}
	ok := len(errs) == 4
	for i := range errs {
		ok = ok && errors.Is(errs[i], want[i])
		for j := range want {
			ok = ok && (i == j || !errors.Is(errs[i], want[j]))
		}
	}
	check("四类可判定错误互不相同", ok)
	// 6. 被拒后状态不变。
	api.New(2)
	api.Intern("x")
	api.Intern("y")
	before := api.Snapshot()
	api.Intern("z")
	api.Release("nope")
	after := api.Snapshot()
	eq := len(before) == len(after)
	for i := range before {
		eq = eq && before[i] == after[i]
	}
	check("被拒后状态不变", eq)
	// 7. 大 m 下驱逐扫描条目数不随 m 增长。
	check("大 m 驱逐扫描不随 m 增长", pool.New(1).SelfCheck() == nil)
	// 8. 并发 Intern/Release 后计数对齐。
	api.New(16)
	keys := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				for _, k := range keys {
					api.Intern(k)
					api.Release(k)
				}
			}
		}()
	}
	wg.Wait()
	snap = api.Snapshot()
	aligned := api.Len() == len(keys) && api.Len() <= 16
	for _, e := range snap {
		aligned = aligned && e.Refs == 0
	}
	check("并发后计数对齐", aligned)
	if failed {
		os.Exit(1)
	}
}
