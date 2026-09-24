// Command demo 校验首值增量维护的各条性质，逐条打印 OK/FAIL。
// 不读参数、不联网；全部通过退出码为 0，否则为 1。
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"sync"

	"ontology/api"
	"ontology/first"
)

var allOK = true

func report(ok bool, msg string) {
	tag := "OK"
	if !ok {
		tag, allOK = "FAIL", false
	}
	fmt.Printf("%s %s\n", tag, msg)
}

func label(k string, ts int64, ok bool) string {
	if !ok {
		return "-"
	}
	return fmt.Sprintf("%s@%d", k, ts)
}

func main() {
	// 第三节八步，逐步记录首值。
	t := api.New(8)
	ops := []struct {
		add bool
		k   string
		ts  int64
	}{
		{true, "a", 5}, {true, "b", 5}, {true, "a", 3}, {true, "a", 3},
		{false, "a", 3}, {false, "a", 3}, {false, "a", 5}, {false, "b", 5},
	}
	got := make([]string, 8)
	for i, o := range ops {
		var err error
		if o.add {
			err = t.Add(o.k, o.ts)
		} else {
			err = t.Remove(o.k, o.ts)
		}
		if err != nil {
			report(false, fmt.Sprintf("step %d unexpected error %v", i+1, err))
		}
		k, ts, ok := t.First()
		got[i] = label(k, ts, ok)
	}
	want := []string{"a@5", "a@5", "a@3", "a@3", "a@3", "a@5", "b@5", "-"}
	report(strings.Join(got, " ") == strings.Join(want, " "),
		fmt.Sprintf("八步首值序列: %s", strings.Join(got, " ")))
	report(got[1] == "a@5", "第2步 TS 并列按 Key 升序: "+got[1])
	report(got[4] == "a@3", "第5步重复值撤一次仍在: "+got[4])
	report(got[5] == "a@5", "第6步撤末值次首提升: "+got[5])

	// 三类可判定、互不相同的错误。
	e1, e2 := api.New(1).Add("", 1), api.New(1).Remove("z", 9)
	c := api.New(1)
	e3 := c.Add("x", 0)
	if e3 != nil {
		e3 = fmt.Errorf("setup failed: %w", e3)
	} else {
		e3 = c.Add("y", 1)
	}
	distinct := errors.Is(e1, api.ErrEmptyKey) && errors.Is(e2, api.ErrNotFound) &&
		errors.Is(e3, api.ErrCapacity) && e1 != e2 && e1 != e3 && e2 != e3
	report(distinct, "三类哨兵错误可判定且互异")

	// 被拒后状态不变，且实例仍可继续使用。
	d := api.New(1)
	d.Add("a", 5)
	bK, bTS, bOK := d.First()
	d.Add("", 1)
	d.Remove("z", 9)
	d.Add("b", 6)
	k, ts, ok := d.First()
	unchanged := ok == bOK && k == bK && ts == bTS && d.Count() == 1
	d.Remove("a", 5)
	usable := d.Add("b", 6) == nil // 腾出容量后可继续正常加入
	report(unchanged && usable, "被拒操作不留痕且实例可继续使用")

	// 大 m 下 Add 的堆比较次数不随 m 线性增长（first 内部计数器，仅回结论）。
	report(first.VerifyComplexity(), "大 m(100/1000/10000) 比较次数 O(log m)")

	// 并发只读：多个 goroutine 拿到的首值逐字段相同（无 sleep）。
	r := api.New(1 << 20)
	for _, p := range rand.New(rand.NewSource(7)).Perm(500) {
		r.Add("k", int64(p))
	}
	wK, wTS, wOK := r.First()
	var wg sync.WaitGroup
	var mu sync.Mutex
	bad := false
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				k, ts, ok := r.First()
				if k != wK || ts != wTS || ok != wOK || r.Count() != 500 || !r.SelfCheck() {
					mu.Lock()
					bad = true
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	report(!bad, "16 goroutine 并发只读首值逐字段相同")

	if !allOK {
		os.Exit(1)
	}
}
