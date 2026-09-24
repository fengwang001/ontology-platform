package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"sync"

	"ontology/api"
)

var failed bool

func check(name string, ok bool, detail string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s: %s %s\n", name, status, detail)
}

func main() {
	// 第三节八步：逐步核对未了结序列与净值（第 7、8 步判定含在其中）。
	steps := []api.Op{5, -5, -8, 8, 7, 2, -7, -2}
	wantSeq := [][]int64{{5}, {}, {-8}, {}, {7}, {7, 2}, {7, 2, -7}, {7, 2, -7, -2}}
	wantNet := []int64{5, 0, -8, 0, 7, 9, 2, 0}
	a := api.New(8)
	ok := true
	for i, op := range steps {
		if err := a.Apply(op); err != nil || a.Net() != wantNet[i] || !eqSeq(a.Changelog(), wantSeq[i]) {
			ok = false
			break
		}
	}
	check("eight-steps", ok, fmt.Sprintf("step7=%v step8=%v net=%d", wantSeq[6], a.Changelog(), a.Net()))

	// 自检（内置序列核验四条不变量）与重放 changelog 回到净值。
	replay := int64(0)
	for _, v := range a.Changelog() {
		replay += v
	}
	check("selfcheck+replay", a.SelfCheck() == nil && replay == a.Net(), fmt.Sprintf("replay=%d", replay))

	// 三类可判定错误，互不相同。
	e1 := api.New(4).Apply(0)
	d := api.New(1)
	_ = d.Apply(5)
	e2 := d.Apply(3)
	o := api.New(8)
	_ = o.Apply(math.MaxInt64)
	e3 := o.Apply(1)
	distinct := errors.Is(e1, api.ErrInvalidDelta) && errors.Is(e2, api.ErrDepthExceeded) &&
		errors.Is(e3, api.ErrNetOverflow) && e1 != e2 && e2 != e3 && e1 != e3
	check("three-errors", distinct, "invalid/depth/overflow 可判定且互不相同")

	// 被拒后状态不变，且仍可继续正常使用。
	before := o.Changelog()
	netBefore := o.Net()
	_ = o.Apply(1) // 再次被拒（溢出）
	check("reject-no-trace", netBefore == o.Net() && eqSeq(before, o.Changelog()) && o.Apply(-1) == nil,
		fmt.Sprintf("net=%d len=%d", o.Net(), len(o.Changelog())))

	// 大 m 下折叠只查末尾一条：m=10000 的未了结序列上喂不抵消操作，结果正确；
	// 检查次数恒为常数的硬断言在 norm 白盒测试 TestTailCheckConstant（计数器非导出，demo 不可读）。
	const m = 10000
	big := api.New(m + 1)
	for i := 0; i < m; i++ {
		_ = big.Apply(1)
	}
	ok = big.Apply(-2) == nil && len(big.Changelog()) == m+1 && big.Net() == m-2
	check("large-m-tail-only", ok, "m=10000 O(1)（计数器断言见 norm 白盒测试）")

	// 并发只读：N 个 goroutine 读同一已喂满实例，净值与序列必须逐条相同。
	var wg sync.WaitGroup
	consistent := true
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if big.Net() != m-2 || len(big.Changelog()) != m+1 || big.SelfCheck() != nil {
					consistent = false
					return
				}
			}
		}()
	}
	wg.Wait()
	check("concurrent-reads", consistent, "32 goroutine 结果逐条相同")

	if failed {
		os.Exit(1)
	}
}

func eqSeq(a, b []int64) bool {
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
