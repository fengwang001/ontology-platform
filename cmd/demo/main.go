// Command demo 运行分组多列精确 distinct 计数的内置判定，全部通过时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/dd"
	"ontology/tup"
)

func main() {
	fails := 0
	check := func(name string, ok bool) {
		if ok {
			fmt.Println("OK  " + name)
		} else {
			fmt.Println("FAIL " + name)
			fails++
		}
	}

	// tup：引用计数 0↔1 翻转才改 distinct，降到 0 的元组被移除。
	cat := tup.NewCatalog()
	t := tup.T{C1: "a", C2: 10}
	b1, a1 := cat.Adjust("k", t, +1)
	b2, a2 := cat.Adjust("k", t, +1)
	_, a3 := cat.Adjust("k", t, -1)
	_, a4 := cat.Adjust("k", t, -1)
	check("tup flip 0->1->2->1->0 distinct=0",
		b1 == 0 && a1 == 1 && b2 == 1 && a2 == 2 && a3 == 1 && a4 == 0 &&
			cat.Distinct("k") == 0 && cat.RefCount("k", t) == 0 && cat.Total() == 0)

	// dd：第三节八步，每步后的 Distinct("k") 须为 [1 2 3 3 4 3 2 2]，终值 2。
	e := dd.NewEngine()
	type op struct {
		del bool
		id  int
		c1  string
		c2  int
	}
	ops := []op{
		{false, 1, "a", 10}, {false, 2, "a", 20}, {false, 3, "b", 10},
		{false, 4, "a", 10}, {false, 1, "a", 30}, {true, 4, "", 0},
		{false, 2, "b", 10}, {true, 3, "", 0},
	}
	want := []int{1, 2, 3, 3, 4, 3, 2, 2}
	got := make([]int, 8)
	for i, o := range ops {
		var err error
		if o.del {
			err = e.Delete(o.id)
		} else {
			err = e.Upsert(o.id, "k", o.c1, o.c2)
		}
		if err != nil {
			fails++
		}
		got[i] = e.Distinct("k")
	}
	check(fmt.Sprintf("8-step distinct=%v final=2 total=2", got),
		fmt.Sprint(got) == fmt.Sprint(want) && e.Distinct("k") == 2 && e.Total() == 2)

	// dd：三类错误互不相同；被拒后状态不变且引擎仍可正常使用。
	e2 := dd.NewEngine()
	e2m0 := e2.Distinct("k")
	errDel := e2.Delete(9)
	errKey := e2.Upsert(1, "", "a", 1)
	errID := e2.Upsert(0, "k", "a", 1)
	distinctErrs := errors.Is(errDel, dd.ErrRowNotFound) &&
		errors.Is(errKey, dd.ErrEmptyKey) && errors.Is(errID, dd.ErrInvalidRowID) &&
		errDel.Error() != errKey.Error() && errKey.Error() != errID.Error() &&
		errDel.Error() != errID.Error()
	_ = e2m0
	check("3 distinct sentinel errors", distinctErrs)
	check("rejected ops leave no trace, still usable",
		e2.Distinct("k") == 0 && e2.Total() == 0 && e2.Upsert(7, "k", "a", 1) == nil &&
			e2.Distinct("k") == 1 && e2.Delete(7) == nil && e2.Distinct("k") == 0)

	// dd：SelfCheck 核验四条不变量，内含 m=100/1000/10000 复杂度探针。
	check("SelfCheck: batch-consistent + O(1) tuple checks", e.SelfCheck() == nil &&
		dd.NewEngine().SelfCheck() == nil)

	// api：N 个 goroutine 各 Upsert 唯一 rowID 与两两不同元组；结束后 Distinct==N，
	// 期间并发读到的 Distinct 单调不减（用轮询屏障同步，不用 sleep）。
	c := api.New()
	const N = 64
	var wg sync.WaitGroup
	start := make(chan struct{})
	stop := make(chan struct{})
	var mono int32 = 1 // 用原子操作记录读者是否观察到下降
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			prev := 0
			for {
				select {
				case <-stop:
					return
				default:
					d := c.Distinct("k")
					if d < prev {
						atomic.StoreInt32(&mono, 0)
					}
					prev = d
				}
			}
		}()
	}
	var pwg sync.WaitGroup
	for i := 1; i <= N; i++ {
		pwg.Add(1)
		go func(id int) {
			defer pwg.Done()
			<-start
			_ = c.Upsert(id, "k", "c", id)
		}(i)
	}
	close(start)
	pwg.Wait()
	close(stop)
	wg.Wait()
	check("api concurrent upsert: distinct==N, reads monotonic non-decreasing",
		c.Distinct("k") == N && c.Total() == N && atomic.LoadInt32(&mono) == 1 &&
			c.SelfCheck() == nil && errors.Is(c.Delete(1<<20), api.ErrRowNotFound))

	if fails != 0 {
		panic("demo failed")
	}
}
