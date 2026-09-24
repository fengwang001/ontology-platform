// Command demo 逐项核验迟到率自适应水位线的行为，全部通过时退出码为 0。
package main

import (
	"fmt"
	"os"
	"sync"

	"ontology/adapt"
	"ontology/api"
	"ontology/wmline"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Printf("%s: %s\n", name, map[bool]string{true: "OK", false: "FAIL"}[ok])
}

func main() {
	// 1. wmline：前三步 wm 与第 3 步 TS==wm 的边界判定（等于不算迟到）
	l := wmline.New(2, 10)
	l1 := l.Observe(10)
	wm1 := l.WM()
	l2 := l.Observe(11)
	l3 := l.Observe(9) // TS==wm==9，严格小于才算迟到
	check("wmline boundary(step3 TS==wm not late)", !l1 && wm1 == 8 && !l2 && !l3 && l.WM() == 9)

	// 2. adapt：大 m 窗口结算结果与 m 无关（O(1) 读计数的行为佐证，计数器断言在 adapt 白盒测试）
	ok := true
	for _, m := range []int64{100, 1000, 10000} {
		c := adapt.New(wmline.New(2, 10), 2, m, 2, 0)
		for i := int64(0); i < m; i++ {
			c.Feed(i)
		}
		ok = ok && c.Delay() == 2
	}
	check("adapt settle O(1) for m=100..10000", ok)

	// 3. api：九事件分步轨迹（含第 6 步上调到 4、第 9 步回落到 2）
	st, _ := api.New(2, 10, 2, 3, 2, 0)
	type row struct{ ts, wm, delay int64 }
	want := []row{{10, 8, 2}, {11, 9, 2}, {9, 9, 2}, {5, 9, 2}, {6, 9, 2}, {15, 13, 4}, {16, 12, 4}, {17, 13, 4}, {18, 14, 2}}
	ok = true
	for i, r := range want {
		st.Feed(r.ts)
		if st.WM() != r.wm || st.Delay() != r.delay {
			fmt.Printf("  step%d: got wm=%d delay=%d, want wm=%d delay=%d\n", i+1, st.WM(), st.Delay(), r.wm, r.delay)
			ok = false
		}
	}
	check("api nine-event trace (up@6 down@9)", ok)

	// 4. 五类可判定错误互不相同 + 被拒后已有实例状态不变
	errs := []error{}
	for _, p := range [][6]int64{{5, 2, 1, 1, 1, 0}, {-1, 2, 1, 1, 1, 0}, {0, 2, 0, 1, 1, 0}, {0, 2, 1, 0, 1, 0}, {0, 2, 1, 3, 2, 2}} {
		_, err := api.New(p[0], p[1], p[2], p[3], p[4], p[5])
		errs = append(errs, err)
	}
	uniq := map[error]bool{}
	for _, e := range errs {
		uniq[e] = true
	}
	check("api 5 distinct reject errors", len(uniq) == 5 && errs[0] != nil)
	check("api state intact after rejects", st.WM() == 14 && st.Delay() == 2 && !st.Feed(30))

	// 5. 并发只读一致 + 内置自检
	wm, d := st.WM(), st.Delay()
	var wg sync.WaitGroup
	start := make(chan struct{})
	same := make(chan bool, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			same <- st.WM() == wm && st.Delay() == d && st.SelfCheck() == nil
		}()
	}
	close(start)
	wg.Wait()
	ok = true
	for i := 0; i < 16; i++ {
		ok = ok && <-same
	}
	check("api concurrent read/selfcheck consistent", ok)

	if failed {
		os.Exit(1)
	}
}
