// demo 逐项演示 K-slack 乱序事件计数的判定与不变量，全部 OK 时退出码 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/wcount"
)

var fails int64

func check(name string, ok bool) {
	status := "OK  "
	if !ok {
		status = "FAIL"
		atomic.AddInt64(&fails, 1)
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	// 1. 第三节八步序列：逐步核验 high/Accepted/Dropped。
	seqs := []int64{10, 8, 12, 5, 8, 11, 9, 7}
	want := [][3]int64{{10, 1, 0}, {10, 2, 0}, {12, 3, 0}, {12, 3, 1}, {12, 3, 2}, {12, 4, 2}, {12, 5, 2}, {12, 5, 3}}
	w, _ := api.New(3, 4)
	ok := true
	for i, s := range seqs {
		if err := w.Feed([]api.Event{{Key: "a", Seq: s}}); err != nil {
			ok = false
			break
		}
		h, _ := w.High("a")
		if h != want[i][0] || w.Accepted("a") != want[i][1] || w.Dropped() != want[i][2] {
			ok = false
		}
	}
	check("eight-step high/Accepted/Dropped per step", ok)

	// 2. 第 7 步边界：Seq == high-K 左闭接受（上表第 7 行 Accepted 4→5）。
	check("step7 Seq==high-K accepted (left-closed)", w.Accepted("a") == 5 && w.Dropped() == 3)

	// 3. 首个事件规则 + 窗口内乱序接受但不推进 high。
	f, _ := api.New(3, 4)
	_ = f.Feed([]api.Event{{Key: "x", Seq: -50}}) // 首个事件无条件接受，high=-50
	h1, ok1 := f.High("x")
	_ = f.Feed([]api.Event{{Key: "x", Seq: -52}}) // 窗口内乱序：接受但不推进
	h2, _ := f.High("x")
	check("first-event rule & in-window no-advance", ok1 && h1 == -50 && h2 == -50 && f.Accepted("x") == 2)

	// 4. 三类可判定错误互不相同。
	_, e1 := api.New(-1, 1)
	e2 := w.Feed([]api.Event{{Key: "", Seq: 1}})
	e3 := w.Feed([]api.Event{{Key: "k5", Seq: 1}, {Key: "k6", Seq: 2}, {Key: "k7", Seq: 3}, {Key: "k8", Seq: 4}})
	check("three distinct sentinel errors",
		errors.Is(e1, api.ErrBadParam) && errors.Is(e2, api.ErrEmptyKey) && errors.Is(e3, api.ErrTooManyKeys) &&
			e1 != e2 && e2 != e3 && e1 != e3)

	// 5. 被拒整批不留痕，之后仍可正常使用。
	g, _ := api.New(3, 2)
	_ = g.Feed([]api.Event{{Key: "a", Seq: 10}})
	d0 := g.Dropped()
	err := g.Feed([]api.Event{{Key: "b", Seq: 1}, {Key: "", Seq: 2}})
	hA, _ := g.High("a")
	ok = errors.Is(err, api.ErrEmptyKey) && g.Dropped() == d0 && g.Accepted("b") == 0 && hA == 10
	err = g.Feed([]api.Event{{Key: "b", Seq: 1}, {Key: "c", Seq: 2}})
	ok = ok && errors.Is(err, api.ErrTooManyKeys) && g.Accepted("c") == 0
	ok = ok && g.Feed([]api.Event{{Key: "b", Seq: 1}}) == nil && g.Accepted("b") == 1
	check("rejected batch leaves no trace, still usable", ok)

	// 6. 大 m 下定位检查 Key 个数不随 m 增长（内部断言，不读计数器数值）。
	check("lookup cost independent of key count", wcount.New(3, 10001).SelfCheck())

	// 7. 并发 Feed 互不相同的 Key，结果与串行等价。
	const n = 64
	cw, _ := api.New(3, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			k := fmt.Sprintf("g%d", i)
			_ = cw.Feed([]api.Event{{Key: k, Seq: 10}, {Key: k, Seq: 9}, {Key: k, Seq: 12}, {Key: k, Seq: 5}})
		}(i)
	}
	wg.Wait()
	ok = cw.Dropped() == n
	for i := 0; i < n; i++ {
		h, _ := cw.High(fmt.Sprintf("g%d", i))
		ok = ok && h == 12 && cw.Accepted(fmt.Sprintf("g%d", i)) == 3
	}
	check("concurrent Feed distinct keys == serial", ok)

	// 8. 并发 High/Accepted/Dropped 同一实例，结果逐字段相同。
	var bad int64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h, _ := cw.High("g0")
			if h != 12 || cw.Accepted("g0") != 3 || cw.Dropped() != n {
				atomic.AddInt64(&bad, 1)
			}
		}(i)
	}
	wg.Wait()
	check("concurrent readers identical", atomic.LoadInt64(&bad) == 0)

	// 9. 内置自检（四条不变量）。
	check("SelfCheck", cw.SelfCheck() == nil)

	if atomic.LoadInt64(&fails) > 0 {
		os.Exit(1)
	}
}
