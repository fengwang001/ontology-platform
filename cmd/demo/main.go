// Command demo 逐条核验二级索引维护的各项判定，全部 OK 时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"slices"
	"sync"
	"sync/atomic"

	"ontology/api"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Printf("OK   %s\n", name)
	} else {
		fmt.Printf("FAIL %s\n", name)
		failed = true
	}
}

func mustApply(d *api.DB, ops ...api.Op) {
	if err := d.Apply(ops); err != nil {
		panic(err)
	}
}

func mustRange(d *api.DB, lo, hi int64) []string {
	got, err := d.Range(lo, hi)
	if err != nil {
		panic(err)
	}
	return got
}

func main() {
	d := api.New() // 1. 第三节八步序列：F=5/F=8 索引组与第 7、8 步查询结果
	mustApply(d, api.Put("a", 5), api.Put("b", 5), api.Put("c", 8))
	mustApply(d, api.Put("a", 8), api.Del("b"), api.Put("d", 5))
	check("eight-steps groups+queries",
		slices.Equal(mustRange(d, 5, 6), []string{"d"}) &&
			slices.Equal(mustRange(d, 8, 9), []string{"a", "c"}) &&
			slices.Equal(mustRange(d, 5, 8), []string{"d"}))
	// 2. Put 更新后旧组不可见；3. F 相同的 Put 不产生重复项。
	d2 := api.New()
	mustApply(d2, api.Put("a", 5), api.Put("a", 9))
	g5, _ := d2.Eq(5)
	g9, _ := d2.Eq(9)
	check("update hides old group", len(g5) == 0 && slices.Equal(g9, []string{"a"}))
	mustApply(d2, api.Put("a", 9))
	g9, _ = d2.Eq(9)
	check("same-F put no dup", slices.Equal(g9, []string{"a"}))
	// 4. Range 严格左闭右开：lo 含、hi 不含、lo>=hi 为空。
	check("range half-open",
		slices.Equal(mustRange(d2, 9, 10), []string{"a"}) &&
			len(mustRange(d2, 9, 9))+len(mustRange(d2, 10, 9)) == 0 &&
			len(mustRange(d2, 10, 11)) == 0)
	// 5. 随机操作序列与批量重算逐查询一致。
	d3, model := api.New(), map[string]int64{}
	r := rand.New(rand.NewSource(7))
	keys := []string{"a", "b", "c", "d", "e", "f"}
	for i := 0; i < 400; i++ {
		op := api.Put(keys[r.Intn(6)], int64(r.Intn(5)))
		if r.Intn(3) == 0 {
			op = api.Del(keys[r.Intn(6)])
		}
		_, ok := model[op.PK]
		err := d3.Apply([]api.Op{op})
		if op.Del && !ok && errors.Is(err, api.ErrNotFound) {
			continue
		}
		if err != nil {
			panic(err)
		}
		if op.Del {
			delete(model, op.PK)
		} else {
			model[op.PK] = op.F
		}
	}
	consistent := true
	for f := int64(0); f < 5; f++ {
		var want []string
		for k, v := range model {
			if v == f {
				want = append(want, k)
			}
		}
		slices.Sort(want)
		got, _ := d3.Eq(f)
		consistent = consistent && slices.Equal(got, want)
	}
	check("batch recompute consistent", consistent)
	// 6. 三类可判定且互不相同的错误；7. 被拒后状态不变。
	before := mustRange(d3, -10, 100)
	e1 := d3.Apply([]api.Op{api.Put("", 1)})
	e2 := d3.Apply([]api.Op{api.Del("ghost")})
	e3 := d3.Apply([]api.Op{api.Put("z", 1), api.Del("ghost")})
	check("three distinct errors",
		errors.Is(e1, api.ErrEmptyPK) && !errors.Is(e1, api.ErrNotFound) && !errors.Is(e1, api.ErrBatch) &&
			errors.Is(e2, api.ErrNotFound) && !errors.Is(e2, api.ErrEmptyPK) && !errors.Is(e2, api.ErrBatch) &&
			errors.Is(e3, api.ErrBatch) && errors.Is(e3, api.ErrNotFound))
	check("rejected batch no trace", slices.Equal(before, mustRange(d3, -10, 100)))
	// 8. 大 m 下单命中 Range 结果正确（检查个数的对数上界由 ient 包内测试断言）。
	d4 := api.New()
	for i := 0; i < 10000; i++ {
		mustApply(d4, api.Put(fmt.Sprintf("k%05d", i), int64(i*2)))
	}
	check("large-m single-hit range", slices.Equal(mustRange(d4, 12340, 12342), []string{"k06170"}))
	// 9. 并发：N 个只读 goroutine 结果逐元素相同，另一 goroutine 循环 Apply 合法操作。
	var wg sync.WaitGroup
	var same atomic.Bool
	same.Store(true)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rr := rand.New(rand.NewSource(seed))
			for i := 0; i < 50; i++ {
				f := int64(rr.Intn(20000))
				got := mustRange(d4, f, f+1)
				var want []string
				if f%2 == 0 && f/2 < 10000 {
					want = []string{fmt.Sprintf("k%05d", f/2)}
				}
				if !slices.Equal(got, want) {
					same.Store(false)
				}
			}
		}(int64(g))
	}
	wg.Add(1)
	go func() { // 写操作只触碰 F=10^9 的键，读者查询范围不可见
		defer wg.Done()
		for i := 0; i < 200; i++ {
			mustApply(d4, api.Put(fmt.Sprintf("w%04d", i), 1_000_000_000))
		}
	}()
	wg.Wait()
	check("concurrent readers consistent", same.Load())
	check("SelfCheck", api.New().SelfCheck() == nil) // 10. 内置自检覆盖四条不变量
	if failed {
		os.Exit(1)
	}
}
