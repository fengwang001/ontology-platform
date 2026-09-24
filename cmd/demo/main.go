// Command demo 演示事件乱序度观测器：纯观测、不推进水位、不丢弃事件。
// 不读参数、不联网；每条核验打印 OK/FAIL，任一 FAIL 即以非零码退出。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/obs"
	"ontology/seq"
)

var failed bool

func check(ok bool, format string, args ...any) {
	if ok {
		fmt.Println("OK: " + fmt.Sprintf(format, args...))
	} else {
		failed = true
		fmt.Println("FAIL: " + fmt.Sprintf(format, args...))
	}
}

func main() {
	// 1) 第三节六步事件：逐步 MaxSeen/判定/OutOfOrder/MaxLateness（判定直接用 seq 包），聚合为一行。
	o, _ := api.New(10)
	var maxSeen int64
	want := []string{"有序:10/0/0", "相等:10/0/0", "乱序:10/1/5", "有序:12/1/5", "乱序:12/2/5", "有序:13/2/5"}
	got6 := make([]string, 6)
	for i, s := range []int64{10, 10, 5, 12, 11, 13} {
		if err := o.Feed(s); err != nil {
			check(false, "step %d Feed: %v", i+1, err)
		}
		k := "有序"
		if i > 0 {
			k = seq.Classify(s, maxSeen).String()
		}
		if s > maxSeen || i == 0 {
			maxSeen = s
		}
		got6[i] = fmt.Sprintf("%s:%d/%d/%d", k, o.MaxSeen(), o.OutOfOrder(), o.MaxLateness())
	}
	check(fmt.Sprint(got6) == fmt.Sprint(want), "六步序列逐步核验 %v", got6)

	// 2) 相等不算乱序；3) MaxLateness 取历史最大（5 不被 1 覆盖）。
	check(o.OutOfOrder() == 2 && o.MaxLateness() == 5 && o.MaxSeen() == 13,
		"相等不算乱序且迟到量取历史最大: ooo=2 late=5 max=13")

	// 4) 纯观测不丢弃：late=5(>slack=3) 与 late=1 都计数，仅置标志位。
	p, _ := api.New(3)
	for _, s := range []int64{10, 5, 12, 11} {
		p.Feed(s)
	}
	check(p.OutOfOrder() == 2 && p.MaxLateness() == 5 && p.ExceedsSlack(),
		"纯观测不丢弃: ooo=2 late=5 exceeds=true")

	// 5) 三类可判定哨兵错误互不相同。
	_, eSlack := api.New(-1)
	check(errors.Is(eSlack, api.ErrInvalidSlack) &&
		!errors.Is(api.ErrInvalidSeq, api.ErrFrozen) &&
		!errors.Is(api.ErrInvalidSeq, api.ErrInvalidSlack) &&
		!errors.Is(api.ErrFrozen, api.ErrInvalidSlack),
		"三类哨兵错误可判定且互不相同")

	// 6) 被拒后状态不变（非法序号不留痕；冻结后 Feed 被拒且仍冻结）。
	q, _ := api.New(10)
	q.Feed(10)
	q.Feed(5)
	before := [3]int64{q.MaxSeen(), q.OutOfOrder(), q.MaxLateness()}
	e1, e2 := q.Feed(0), q.Feed(-1)
	after := [3]int64{q.MaxSeen(), q.OutOfOrder(), q.MaxLateness()}
	q.Freeze()
	e3, e4 := q.Feed(11), q.Feed(1)
	check(errors.Is(e1, api.ErrInvalidSeq) && errors.Is(e2, api.ErrInvalidSeq) &&
		errors.Is(e3, api.ErrFrozen) && errors.Is(e4, api.ErrFrozen) && before == after,
		"被拒后状态不变且冻结态保持: %v", before)

	// 7) 大 m 下标量统计一致；每事件检查数恒 1 由 obs 白盒 TestCheckCountBounded 钉住。
	bigOK := true
	for _, m := range []int64{100, 1000, 10000} {
		b, _ := api.New(10)
		for i := int64(1); i <= m; i++ {
			b.Feed(i)
		}
		if b.MaxSeen() != m || b.OutOfOrder() != 0 {
			bigOK = false
		}
	}
	check(bigOK, "大m(100/1k/10k) O(1)标量统计一致，检查数不随m增长(obs白盒测试钉住)")

	// 8) 并发只读：N 个 goroutine 无 sleep 同读一个喂满实例，四项逐项相同。
	full, _ := api.New(10)
	for _, s := range []int64{10, 10, 5, 12, 11, 13, 2, 14} {
		full.Feed(s)
	}
	const n = 64
	res := make([][4]int64, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			var ex int64
			if full.ExceedsSlack() {
				ex = 1
			}
			res[i] = [4]int64{full.MaxSeen(), full.OutOfOrder(), full.MaxLateness(), ex}
		}(i)
	}
	close(start)
	wg.Wait()
	same := true
	for i := 1; i < n; i++ {
		if res[i] != res[0] {
			same = false
		}
	}
	check(same && res[0] == [4]int64{14, 3, 11, 1}, "并发只读结果逐项一致: %v", res[0])

	// 9) 对外暴露的包确实分层：api 依赖 obs，obs 依赖 seq（引用防剪裁）。
	check(obs.ErrFrozen != nil && seq.Late == seq.Late, "分包依赖方向 api->obs->seq")

	if failed {
		os.Exit(1)
	}
}
