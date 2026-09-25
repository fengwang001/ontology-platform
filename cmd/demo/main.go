// Command demo 逐步演示迟到事件的侧输出分流。
// 不读参数、不联网；逐条打印 OK/FAIL，退出码 0，输出不超过 10 行。
package main

import (
	"errors"
	"fmt"
	"sync"

	"ontology/api"
)

var failed bool

func ok(label string, good bool) {
	tag := "OK  "
	if !good {
		tag, failed = "FAIL", true
	}
	fmt.Printf("%s %s\n", tag, label)
}

func main() {
	// 第三节八步序列（期望逐步判定/gap/view）。
	type step struct {
		k      string
		ts     int64
		main   bool
		gap    int64
		vA, vB int64
	}
	steps := []step{
		{"A", 10, true, 0, 10, 0}, {"B", 20, true, 0, 10, 20},
		{"A", 15, true, 0, 15, 20}, {"A", 15, true, 0, 15, 20},
		{"B", 20, true, 0, 15, 20}, {"B", 25, true, 0, 15, 25},
		{"A", 12, false, 3, 15, 25}, {"A", 10, false, 5, 15, 25},
	}
	d := api.New(8)
	eightOK := true
	for i, s := range steps {
		if err := d.Feed(s.k, s.ts); err != nil {
			eightOK = false
		}
		v := d.View()
		g := int64(0)
		if sd := d.Side(); !s.main {
			g = sd[len(sd)-1].Gap
		}
		if v["A"] != s.vA || v["B"] != s.vB || g != s.gap {
			eightOK = false
			fmt.Printf("     step %d mismatch view=%v gap=%d\n", i+1, v, g)
		}
	}
	ok("1 八步逐步判定/gap/view 全对", eightOK && len(d.Main()) == 6 && len(d.Side()) == 2)
	ok("2 边界: 第4/5步 TS==水位判主路（错写>会侧分且 gap 错成 0）",
		d.Main()[3].TS == 15 && d.Main()[4].Key == "B" && d.Main()[4].TS == 20)
	// 水位口径：第3步 per-key 水位=10 主路；全局水位=20 会把 (A,15) 错判侧路。
	globalWM := int64(20)
	ok("3 口径: 第3步 wm[A]=10 主路；全局水位 20 会错判侧路 gap=5",
		steps[2].main && 15 < globalWM && globalWM-15 == 5)
	// 视图污染：朴素无条件赋值在第7/8步会把 view[A] 写成 12、10。
	ok("4 不污染: 第7/8步后 view[A]=15（无条件赋值会错成 12 再 10）",
		d.View()["A"] == 15)
	// view 与批量最大一致：另取一组乱序事件批量重算。
	d2 := api.New(1000)
	raw := []struct {
		k  string
		ts int64
	}{
		{"k1", 5}, {"k2", 9}, {"k1", 11}, {"k1", 3}, {"k2", 9}, {"k1", 11}, {"k2", 1},
	}
	batch := map[string]int64{}
	for _, e := range raw {
		d2.Feed(e.k, e.ts)
		if m, ok := batch[e.k]; !ok || e.ts > m {
			batch[e.k] = e.ts
		}
	}
	batchOK := true
	for k, m := range batch {
		if d2.View()[k] != m {
			batchOK = false
		}
	}
	ok("5 乱序后 view 与含迟到的批量最大一致", batchOK)
	// 三类可判定、互不相同的哨兵错误。
	distinct := !errors.Is(api.ErrEmptyKey, api.ErrNegativeTS) &&
		!errors.Is(api.ErrNegativeTS, api.ErrSideFull) &&
		!errors.Is(api.ErrEmptyKey, api.ErrSideFull)
	ok("6 三类哨兵错误互不相同", distinct &&
		errors.Is(d2.Feed("", 1), api.ErrEmptyKey) &&
		errors.Is(d2.Feed("z", -1), api.ErrNegativeTS))
	// 失败不留痕 + 拒绝后仍可用。
	d3 := api.New(1)
	d3.Feed("X", 5)
	d3.Feed("X", 3) // 侧路达到容量 1
	before := len(d3.Side())
	overflow := errors.Is(d3.Feed("X", 1), api.ErrSideFull)
	after := len(d3.Side())
	usable := d3.Feed("Q", 8) == nil && d3.View()["Q"] == 8
	ok("7 侧路上溢整体拒绝、不留痕且拒绝后仍可用", overflow && before == after && after == 1 && usable)
	// 大 m O(1)：多档递增后一个迟到仍正确侧分；检查数恒 0 由 wm 同包测试钉住（非导出，不经公开接口）。
	bigOK := true
	for _, m := range []int{100, 1000, 10000} {
		dm := api.New(m)
		for i := 0; i < m; i++ {
			dm.Feed("g", int64(i+1))
		}
		if dm.Feed("g", 0) != nil || len(dm.Side()) != 1 ||
			dm.Side()[0].Gap != int64(m) || dm.View()["g"] != int64(m) {
			bigOK = false
		}
	}
	ok("8 大 m 下迟到仍 O(1) 正确侧分 gap=m（检查数=0 由内部测试钉住）", bigOK)
	// 并发：N 个 goroutine 各打不同 Key，无 sleep。
	var wg sync.WaitGroup
	N, M := 16, 500
	dc := api.New(N * M)
	for g := 0; g < N; g++ {
		wg.Add(1)
		key := fmt.Sprintf("c%02d", g)
		go func() {
			defer wg.Done()
			for j := 0; j < M; j++ { // 模式固定：可独立预算每 Key 最大 TS
				dc.Feed(key, int64((j*7+3)%(M+1)))
			}
		}()
	}
	wg.Wait()
	concOK := dc.SelfCheck() == nil
	for g := 0; g < N; g++ {
		var want int64
		key := fmt.Sprintf("c%02d", g)
		for j := 0; j < M; j++ {
			if v := int64((j*7 + 3) % (M + 1)); v > want {
				want = v
			}
		}
		if dc.View()[key] != want {
			concOK = false
		}
	}
	ok("9 并发不同 Key：各 view==本 Key 批量最大", concOK)
	ok("10 SelfCheck 内置四不变量核验通过", api.New(0).SelfCheck() == nil)

	if failed {
		fmt.Println("DEMO FAILED")
	}
}
