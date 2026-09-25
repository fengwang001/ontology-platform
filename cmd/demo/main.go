// Command demo 逐条演示物化视图乐观并发提交器的语义与不变量。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
)

var failed bool

func ok(name string, cond bool) {
	if cond {
		fmt.Println("OK   " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
	}
}

func main() {
	c, _ := api.New(3)
	sc := c.SelfCheck() == nil // 内部确定性自检：六步交错 / 版本+1 / 大m / 不留痕
	ok("六步交错 (0,0,0)>(0,0,0)>(1,0,1)>(1,0,1)>(1,0,1)>(1,5,11) 重试1", sc)
	ok("版本单调且恰 +1", sc)
	ok("大 m 下校验个数不随 m 增长", sc)

	// (甲) 去掉版本校验：读快照后直接覆盖，不校验不重试。
	val := map[string]int64{}
	g1s := map[string]int64{"a": 0, "c": 0} // 步1 G1 快照
	g2s := map[string]int64{"b": 0, "c": 0} // 步2 G2 快照
	for k, d := range map[string]int64{"a": 1, "c": 1} {
		val[k] = g1s[k] + d // 步3
	}
	after3 := [3]int64{val["a"], val["b"], val["c"]}
	for k, d := range map[string]int64{"b": 5, "c": 10} {
		val[k] = g2s[k] + d // 步4：过期快照覆盖 c
	}
	ok("(甲) 去版本校验: 步3后(1,0,1) 步4后(1,5,10), c 错成 10",
		after3 == [3]int64{1, 0, 1} && val["a"] == 1 && val["b"] == 5 && val["c"] == 10)

	// (乙) 逐 Key 校验→立即应用，冲突不回滚：步4 先应用 b，重试时 b 被再加一次。
	val, ver := map[string]int64{}, map[string]uint64{}
	for _, k := range []string{"a", "c"} { // 步3：G1 提交
		val[k]++
		ver[k]++
	}
	net := map[string]int64{"b": 5, "c": 10}
	snapVer := map[string]uint64{"b": 0, "c": 0} // 步2 快照版本
	for _, k := range []string{"b", "c"} {       // 步4：字典序逐 Key
		if ver[k] == snapVer[k] {
			val[k] += net[k]
			ver[k]++
		}
	}
	for _, k := range []string{"b", "c"} { // 重试：b 再次通过校验被重复加
		val[k] += net[k]
		ver[k]++
	}
	ok("(乙) 部分提交: 步4 先应用 b, b 错成 10", val["b"] == 10 && val["c"] == 11)

	// 与朴素重放一致：并发提交的总效果 = 顺序应用各事务净增量。
	cc, _ := api.New(16)
	batches := [][]api.Op{{{Key: "x", Delta: 1}, {Key: "y", Delta: 2}}, {{Key: "x", Delta: 3}},
		{{Key: "y", Delta: -1}, {Key: "z", Delta: 5}}, {{Key: "z", Delta: 1}, {Key: "x", Delta: 1}}}
	exp := map[string]int64{}
	var wg sync.WaitGroup
	for _, b := range batches {
		for _, op := range b {
			exp[op.Key] += op.Delta
		}
		wg.Add(1)
		go func(b []api.Op) { defer wg.Done(); _ = cc.Commit(b) }(b)
	}
	wg.Wait()
	match := len(cc.View()) == len(exp)
	for k, v := range exp {
		match = match && cc.View()[k] == v
	}
	ok("与朴素重放一致", match)

	// 四类可判定错误，互不相同。
	_, e0 := api.New(0)
	e1 := c.Commit(nil)
	e2 := c.Commit([]api.Op{{Key: "", Delta: 1}})
	e3 := c.Commit([]api.Op{{Key: "k"}})
	e4 := c.Commit([]api.Op{{Key: "k", Delta: 1}, {Key: "k", Delta: -1}}) // 归并后为空
	ok("四类错误可判定且互不相同", errors.Is(e0, api.ErrBadConfig) && errors.Is(e1, api.ErrEmptyBatch) &&
		errors.Is(e2, api.ErrEmptyKey) && errors.Is(e3, api.ErrZeroDelta) && errors.Is(e4, api.ErrEmptyBatch) &&
		e0 != e1 && e1 != e2 && e2 != e3 && e1 != e3)

	// 被拒后状态不变，之后仍可正常使用。
	before, r0 := c.View(), c.Retries()
	_ = c.Commit(nil)
	_ = c.Commit([]api.Op{{Key: "", Delta: 1}})
	same := len(c.View()) == len(before) && c.Retries() == r0
	for k, v := range before {
		same = same && c.View()[k] == v
	}
	ok("被拒后状态不变，可继续使用", same && c.Commit([]api.Op{{Key: "k", Delta: 2}}) == nil)

	// 并发同 Key 不丢更新；并发只读者看到 Retries 单调不减。
	hot, _ := api.New(64)
	const N = 64
	var mono atomic.Bool
	mono.Store(true)
	done := make(chan struct{})
	var wr, rd sync.WaitGroup
	for i := 0; i < 8; i++ {
		rd.Add(1)
		go func() {
			defer rd.Done()
			prev := int64(0)
			for {
				select {
				case <-done:
					return
				default:
					if r := hot.Retries(); r < prev {
						mono.Store(false)
					} else {
						prev = r
					}
					_ = hot.View()
					_ = hot.SelfCheck()
				}
			}
		}()
	}
	for i := 0; i < N; i++ {
		wr.Add(1)
		go func() { defer wr.Done(); _ = hot.Commit([]api.Op{{Key: "hot", Delta: 1}}) }()
	}
	wr.Wait()
	close(done)
	rd.Wait()
	ok("并发同 Key 不丢更新且 Retries 单调", hot.View()["hot"] == N && mono.Load())

	if failed {
		os.Exit(1)
	}
}
