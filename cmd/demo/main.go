package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/hagg"
	"ontology/hop"
)

var failed bool

func check(name string, ok bool, detail ...any) {
	if ok {
		fmt.Println("OK  " + name)
	} else {
		failed = true
		fmt.Println("FAIL "+name, detail)
	}
}

func main() {
	// hop：第 1、2、3、5 步的窗口归属（size=12, slide=4）
	check("assign steps 1/2/3/5",
		reflect.DeepEqual(hop.Starts(-5, 12, 4), []int64{-16, -12, -8}) &&
			reflect.DeepEqual(hop.Starts(0, 12, 4), []int64{-8, -4, 0}) &&
			reflect.DeepEqual(hop.Starts(-1, 12, 4), []int64{-12, -8, -4}) &&
			reflect.DeepEqual(hop.Starts(-3, 12, 4), []int64{-12, -8, -4}))
	// hop：负时间戳向下取整（Go 的 / 向零截断会给出 -4/-16 之外的错误起点）
	check("negative ts floor",
		hop.Starts(-5, 12, 4)[0] == -16 && hop.Starts(-13, 12, 4)[0] == -24 &&
			hop.Contains(-5, -16, 12) && !hop.Contains(-5, -4, 12))
	// hagg：第三节九步序列，逐步核对输出与丢弃数
	a := hagg.New(12, 4, 1<<30)
	must := func(err error) {
		if err != nil {
			check("nine-step no error", false, err)
		}
	}
	must(a.Add("K", -5))
	must(a.Add("K", 0))
	must(a.Add("K", -1))
	r4, err := a.Advance(0)
	must(err)
	must(a.Add("K", -3))
	must(a.Add("K", -13))
	r7, err := a.Advance(4)
	must(err)
	must(a.Add("K", 8))
	r9, err := a.Advance(12)
	must(err)
	want4 := []hagg.Result{{Key: "K", Start: -16, End: -4, Count: 1}, {Key: "K", Start: -12, End: 0, Count: 2}}
	want7 := []hagg.Result{{Key: "K", Start: -8, End: 4, Count: 4}}
	want9 := []hagg.Result{{Key: "K", Start: -4, End: 8, Count: 3}, {Key: "K", Start: 0, End: 12, Count: 2}}
	check("nine-step outputs & dropped",
		reflect.DeepEqual(r4, want4) && reflect.DeepEqual(r7, want7) &&
			reflect.DeepEqual(r9, want9) && a.Dropped() == 1,
		r4, r7, r9, a.Dropped())
	// api：SelfCheck 覆盖四条不变量（朴素参照一致 / 归属数 / 有序唯一 / 失败不留痕）
	w, err := api.New(12, 4, 1<<20)
	check("selfcheck: four invariants", err == nil && w.SelfCheck() == nil)
	// api：四类可判定错误互不相同
	e1, e2 := func() error { _, e := api.New(0, 4, 1); return e }(), error(nil)
	c, _ := api.New(12, 4, 4)
	must2 := c.Add("a", 0)
	e2 = c.Add("", 0)
	e3 := c.Add("b", 0)
	_, advErr := c.Advance(5)
	e4 := func() error { _, e := c.Advance(4); return e }()
	check("four distinguishable sentinel errors",
		must2 == nil && advErr == nil &&
			errors.Is(e1, api.ErrInvalidParams) && errors.Is(e2, api.ErrEmptyKey) &&
			errors.Is(e3, api.ErrMaxOpen) && errors.Is(e4, api.ErrClockBack) &&
			!errors.Is(e1, api.ErrEmptyKey) && !errors.Is(e2, api.ErrMaxOpen) &&
			!errors.Is(e3, api.ErrClockBack) && !errors.Is(e4, api.ErrInvalidParams))
	// api：被拒后状态不变（Results/Dropped 快照一致，且仍可正常使用）
	before := fmt.Sprint(c.Results(), c.Dropped())
	c.Add("", 1)
	c.Add("b", 8) // 3 个新窗口，2+3=5 > maxOpen=4，被拒
	c.Advance(4)
	check("state unchanged after rejections",
		fmt.Sprint(c.Results(), c.Dropped()) == before && c.Add("a", 8) == nil)
	// 复杂度：检查个数不随打开窗口数增长（精确断言在 hagg 包测试 TestAdvanceCheckedBounded）
	big, _ := api.New(12, 4, 1<<20)
	for i := 0; i < 10000; i++ {
		big.Add(fmt.Sprintf("k%d", i), 1<<40)
	}
	r, _ := big.Advance(0)
	check("bounded checked count (exact bound in hagg test)", len(r) == 0)
	// 并发：N 个 goroutine 只读同一已 Flush 实例，结果逐字段相同
	fin, _ := api.New(12, 4, 1<<20)
	for i := 0; i < 200; i++ {
		fin.Add(fmt.Sprintf("k%d", i%7), int64(i)*3-100)
	}
	fin.Flush()
	want := fin.Results()
	var wg sync.WaitGroup
	ok := true
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !reflect.DeepEqual(fin.Results(), want) || fin.Dropped() != 0 || fin.SelfCheck() != nil {
				ok = false
			}
		}()
	}
	wg.Wait()
	check("concurrent reads identical", ok)
	if failed {
		os.Exit(1)
	}
	fmt.Println("ALL OK")
}
