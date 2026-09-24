// 命令 demo 逐条打印幂等生产者序号校验各场景的 OK/FAIL，退出码非 0 即有失败。不读参数、不联网，输出不超过 10 行。
package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"ontology/api"
	"ontology/broker"
)

type req struct {
	pid, epoch int64
	part       int
	seq        int64
}

var ten = []req{
	{7, 0, 0, 0}, {7, 0, 1, 0}, {7, 0, 0, 1}, {7, 0, 0, 1}, {7, 0, 0, 3},
	{7, 0, 0, 2}, {7, 0, 0, 0}, {7, 1, 1, 0}, {7, 0, 0, 3}, {7, 1, 0, 0}}

var want = []string{"+0", "+0", "+1", "=1", "OOO", "+2", "EXPD", "+1", "FENCE", "+3"}

func token(off int64, dup bool, err error) string {
	switch {
	case errors.Is(err, broker.ErrInvalid):
		return "INV"
	case errors.Is(err, broker.ErrFenced):
		return "FENCE"
	case errors.Is(err, broker.ErrOutOfOrder):
		return "OOO"
	case errors.Is(err, broker.ErrDuplicateExpired):
		return "EXPD"
	case dup:
		return fmt.Sprintf("=%d", off)
	default:
		return fmt.Sprintf("+%d", off)
	}
}

func main() {
	ok := true
	check := func(name string, pass bool) {
		if !pass {
			ok = false
		}
		fmt.Println(map[bool]string{true: "OK  ", false: "FAIL "}[pass] + name)
	}

	// 十步判定与位点逐条比对。
	a := api.New(2, 2)
	got := make([]string, len(ten))
	for i, r := range ten {
		off, dup, err := a.Produce(r.pid, r.epoch, r.part, r.seq, "v")
		got[i] = token(off, dup, err)
	}
	check("十步判定与位点 "+strings.Join(got, " "), eq(got, want))

	l0, l1 := a.Log(0), a.Log(1)
	check("最终日志 p0=4 条 p1=2 条", len(l0) == 4 && len(l1) == 2 && l0[3].Epoch == 1 && l1[1].Epoch == 1)

	_, _, e1 := a.Produce(-1, 0, 0, 0, "")
	_, _, e2 := a.Produce(7, 0, 0, 3, "") // epoch0 < 当前1 → 围栏
	c := api.New(1, 2)
	_, _, e3 := c.Produce(1, 0, 0, 1, "") // 新 pid 首条 seq!=0 → 乱序
	c.Produce(2, 0, 0, 0, "")
	c.Produce(2, 0, 0, 1, "")
	c.Produce(2, 0, 0, 2, "")
	_, _, e4 := c.Produce(2, 0, 0, 0, "") // seq0 已滑出 W=2 窗口 → 重复已过期
	distinct := errors.Is(e1, broker.ErrInvalid) && errors.Is(e2, broker.ErrFenced) &&
		errors.Is(e3, broker.ErrOutOfOrder) && errors.Is(e4, broker.ErrDuplicateExpired) &&
		e1 != e2 && e2 != e3 && e3 != e4
	check("四类哨兵错误互不相同", distinct)

	f := api.New(1, 4)
	f.Produce(9, 0, 0, 0, "")
	f.Produce(9, 1, 0, 0, "")
	before := len(f.Log(0))
	_, _, fe := f.Produce(9, 0, 0, 1, "")
	check("旧 epoch 围栏且不落盘", errors.Is(fe, broker.ErrFenced) && len(f.Log(0)) == before)

	g := api.New(1, 4)
	_, _, ge := g.Produce(10, 1, 0, 1, "")
	off, _, gerr := g.Produce(10, 0, 0, 0, "") // 若 epoch 被偷升级，此处会围栏
	check("被拒不升级 epoch 且可继续", errors.Is(ge, broker.ErrOutOfOrder) && gerr == nil && off == 0)

	// 6) 大 m：惰性清空的外部语义（检查数不随 m 增长由白盒 TestEpochBumpLazy 断言）。
	const m = 10000
	h := api.New(m, 4)
	largeOK := true
	for p := 0; p < m && largeOK; p++ {
		if _, _, err := h.Produce(11, 0, p, 0, ""); err != nil {
			largeOK = false
		}
	}
	o0, _, x0 := h.Produce(11, 1, 0, 0, "")
	om, _, xm := h.Produce(11, 1, m-1, 0, "")
	_, _, x1 := h.Produce(11, 1, 1, 1, "")
	check("大m=10000 检查数不随m增长（白盒断言≤2）", largeOK && x0 == nil && o0 == 1 && xm == nil && om == 1 && errors.Is(x1, broker.ErrOutOfOrder))

	// 7) 并发：K 个 goroutine 重发同一组 seq，恰好一次落盘且同 seq 位点一致。
	const K, S = 8, 5
	q := api.New(1, S)
	var wg sync.WaitGroup
	offs := make([][S]int64, K)
	for gx := 0; gx < K; gx++ {
		wg.Add(1)
		go func(gx int) {
			defer wg.Done()
			for s := int64(0); s < S; s++ {
				for {
					if o, _, err := q.Produce(12, 0, 0, s, ""); err == nil {
						offs[gx][s] = o
						break
					}
				}
			}
		}(gx)
	}
	wg.Wait()
	concOK := len(q.Log(0)) == S
	for s := 0; s < S && concOK; s++ {
		for gx := 1; gx < K && concOK; gx++ {
			concOK = offs[gx][s] == offs[0][s] && offs[0][s] == int64(s)
		}
	}
	check("并发重复发送恰好一次落盘", concOK)

	// 内置自检：随机请求序列逐条对拍朴素参照 + 其余三条不变量。
	check("随机序列与朴素参照一致、SelfCheck 通过", api.New(3, 2).SelfCheck() == nil)

	if !ok {
		panic("demo checks failed")
	}
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
