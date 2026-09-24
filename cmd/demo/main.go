// 演示程序：逐项核验引用计数回收的规则与不变量，全部 OK 退出码 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/store"
)

var failed bool

func ok(name string, cond bool) {
	if !cond {
		failed = true
	}
	mark := "OK  "
	if !cond {
		mark = "FAIL "
	}
	fmt.Println(mark + name)
}

func main() {
	d := api.New()
	// 第三节八步表：P(10) h1=A h2=A P(20) h3=A R(h1) R(h2) h1.Get()
	steps := []bool{}
	steps = append(steps, d.Publish(10) == nil && d.AliveCount() == 1) // 1: v1=1
	h1, e1 := d.Acquire()
	steps = append(steps, e1 == nil && h1.Refs() == 2 && d.AliveCount() == 1) // 2: v1=2
	h2, e2 := d.Acquire()
	steps = append(steps, e2 == nil && h1.Refs() == 3 && d.AliveCount() == 1)            // 3: v1=3
	steps = append(steps, d.Publish(20) == nil && h1.Refs() == 2 && d.AliveCount() == 2) // 4: v1=2 v2=1
	h3, e3 := d.Acquire()
	steps = append(steps, e3 == nil && h3.Refs() == 2 && h1.Refs() == 2 && d.AliveCount() == 2)           // 5: v2=2
	steps = append(steps, h1.Release() == nil && h2.Refs() == 1 && d.AliveCount() == 2)                   // 6: v1=1
	steps = append(steps, h2.Release() == nil && h2.Refs() == 0 && h3.Refs() == 2 && d.AliveCount() == 1) // 7: v1 回收
	_, errGet := h1.Get()
	steps = append(steps, errors.Is(errGet, api.ErrUseAfterFree) && d.AliveCount() == 1) // 8: use-after-free
	all := true
	for _, s := range steps {
		all = all && s
	}
	ok("八步表: 每步 v1/v2 refs 与 AliveCount 全对", all)

	// 共享不误回收 + 零引用立即回收（新实例上独立验证）。
	d2 := api.New()
	_ = d2.Publish(7)
	ha, _ := d2.Acquire()
	hb, _ := d2.Acquire()
	_ = d2.Publish(8)
	shared := d2.AliveCount() == 2
	if v, err := ha.Get(); err != nil || v != 7 {
		shared = false
	}
	ok("共享状态不误回收(被引用版本 Get 仍返回发布值)", shared)
	_ = ha.Release()
	_ = hb.Release()
	ok("零引用立即回收(refs 归零 AliveCount 立即减一)", d2.AliveCount() == 1)

	// 四类可判定错误互不相同。
	d3 := api.New()
	_, errEmpty := d3.Acquire()
	errNeg := d3.Publish(-1)
	_ = d3.Publish(1)
	hc, _ := d3.Acquire()
	_ = hc.Release()
	errDouble := hc.Release()
	_, errUAF := hc.Get()
	distinct := map[error]bool{errEmpty: true, errNeg: true, errDouble: true, errUAF: true}
	ok("四类哨兵错误可判定且互不相同", errors.Is(errEmpty, api.ErrEmptyStore) &&
		errors.Is(errNeg, api.ErrNegativeValue) && errors.Is(errDouble, api.ErrDoubleRelease) &&
		errors.Is(errUAF, api.ErrUseAfterFree) && len(distinct) == 4)

	// 失败不留痕：被拒前后 AliveCount 与 refs 不变，且可继续正常使用。
	beforeAlive := d3.AliveCount()
	hd, _ := d3.Acquire()
	beforeRefs := hd.Refs()
	_ = d3.Publish(-1)
	_ = hc.Release()
	_, _ = hc.Get()
	ok("被拒操作不改变任何状态", d3.AliveCount() == beforeAlive && hd.Refs() == beforeRefs)
	ok("被拒后仍可正常使用", hd.Release() == nil && d3.AliveCount() == 1)

	ok("大 m 下 Release 检查个数不随 m 增长(O(1))", store.ReleaseCheckedO1())

	// 并发 Acquire/Release：N 个 goroutine 各自获取即释放。
	d4 := api.New()
	_ = d4.Publish(1)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if h, err := d4.Acquire(); err == nil {
				_ = h.Release()
			}
		}()
	}
	wg.Wait()
	ok("并发 Acquire/Release 后 AliveCount 正确", d4.AliveCount() == 1)

	ok("SelfCheck 内置序列核验四条不变量", api.New().SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
