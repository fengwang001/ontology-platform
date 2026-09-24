// Command demo 逐步演示迟到丢弃与更新；输出全部为 OK 时退出码 0，不读参数不联网。
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	_ "unsafe"

	"ontology/api"
	"ontology/wagg"
	"ontology/win"
)

//go:linkname lastCheckedOf ontology/wagg.lastCheckedOf
func lastCheckedOf(a *wagg.Agg) int // 非导出途径：演示检查个数，数值不出 wagg 的导出接口

func enc(kind byte, cs []api.Change) string {
	s := string(kind) + "|"
	for _, c := range cs {
		s += fmt.Sprintf("%c%d:%d,", map[bool]byte{true: '+', false: '-'}[c.Plus], c.Start, c.Count)
	}
	return s
}
func flat(v map[string][]api.WindowCount) map[string]map[int64]int64 {
	m := map[string]map[int64]int64{}
	for k, ws := range v {
		m[k] = map[int64]int64{}
		for _, w := range ws {
			m[k][w.Start] = w.Count
		}
	}
	return m
}

func main() {
	check := func(n string, ok bool) {
		if !ok {
			fmt.Println("FAIL", n)
			os.Exit(1)
		}
		fmt.Println("OK  ", n)
	}
	// 1) win：负时间戳窗口归属（向下取整、左闭右开）与第 6 步边界谓词。
	w0, w1 := win.Assign(-1, 10), win.Assign(-10, 10)
	b := win.Window{Start: 0, End: 10}
	check("win 负时间窗口归属 + wm=end+lateness 归丢弃",
		w0.Start == -10 && w0.End == 0 && w1.Start == -10 && win.Late(15, b) && win.DropLate(15, b, 5) && !win.AcceptLate(15, b, 5))
	// 2) 第三节八步：逐步判定（N 正常 / L 迟到接受 / D 丢弃）与变更日志；3) 每前缀自洽。
	e, _ := api.New(10, 3, 5, 0)
	tss := []int64{2, 9, 15, 4, 18, 5, 22, 7}
	want := []string{"N|", "N|", "N|+0:2,", "L|-0:2,+0:3,", "N|", "D|", "N|", "D|"}
	stepsOK, prefixOK := true, true
	cur := map[string]map[int64]int64{}
	apply := func(cs []api.Change) {
		for _, c := range cs {
			if cur[c.Key] == nil {
				cur[c.Key] = map[int64]int64{}
			}
			if c.Plus {
				cur[c.Key][c.Start] = c.Count
				continue
			}
			if v, ok := cur[c.Key][c.Start]; !ok || v != c.Count {
				prefixOK = false
			}
			delete(cur[c.Key], c.Start)
		}
	}
	for i, ts := range tss {
		cs, err := e.Feed([]api.Event{{Key: "K", TS: ts}})
		if err != nil || enc(want[i][0], cs) != want[i] {
			stepsOK = false
		}
		apply(cs)
	}
	apply(e.Flush())
	check("第三节八步判定与变更逐条正确（含第6步边界丢弃）", stepsOK && e.Dropped() == 2)
	check("变更日志每个前缀自洽（- 恰好撤回现值）", prefixOK && reflect.DeepEqual(flat(e.View()), cur))
	// 4) Flush 后视图 = 只取被接受事件的独立批量重算。
	var wm win.Watermark
	ref := map[string]map[int64]int64{"K": {}}
	for _, ts := range tss {
		w := win.Assign(ts, 10)
		if win.DropLate(wm.Observe(ts, 3), w, 5) {
			continue
		}
		ref["K"][w.Start]++
	}
	check("最终视图 [0,10)=3 [10,20)=2 [20,30)=1，且与批量重算一致", reflect.DeepEqual(flat(e.View()), ref))
	// 5) 三类可判定、互不相同的哨兵错误。
	_, perr := api.New(0, 0, 0, 0)
	_, kerr := e.Feed([]api.Event{{Key: "", TS: 1}})
	oe, _ := api.New(10, 1<<40, 0, 1)
	oe.Feed([]api.Event{{Key: "a", TS: 0}})
	_, oerr := oe.Feed([]api.Event{{Key: "b", TS: 0}, {Key: "c", TS: 10}})
	check("三类错误可判定且互不相同（参数/空Key/超maxOpen）",
		errors.Is(perr, api.ErrInvalidParams) && errors.Is(kerr, api.ErrEmptyKey) && errors.Is(oerr, api.ErrMaxOpen))
	// 6) 被拒批次不留痕，之后实例仍可正常使用。
	v0, d0 := oe.View(), oe.Dropped()
	oe.Feed([]api.Event{{Key: "b", TS: 0}, {Key: "c", TS: 10}})
	_, usable := oe.Feed([]api.Event{{Key: "a", TS: 1}})
	check("被拒后视图/丢弃数不变且实例仍可用", reflect.DeepEqual(oe.View(), v0) && oe.Dropped() == d0 && usable == nil)
	// 7) 大 m 下检查个数不随 m 增长（非导出 linkname 读取，不经导出 API）。
	subOK, base := true, -1
	for _, m := range []int{100, 1000, 10000} {
		a, evs := wagg.New(1, 1<<40, 0, 0), make([]wagg.Event, m)
		for i := range evs {
			evs[i] = wagg.Event{Key: "K", TS: int64(i)}
		}
		a.Feed(evs)
		a.Feed([]wagg.Event{{Key: "K", TS: int64(m)}}) // 水位线只 +1，不触发窗口
		n := lastCheckedOf(a)
		if n > 2 || (base >= 0 && n != base) {
			subOK = false
		}
		base = n
	}
	check("大 m 下检查个数恒为小常数、不随 m 增长", subOK)
	// 8) N 个 goroutine 并发只读同一喂满实例，结果逐字段相同（无 sleep）。
	ce, _ := api.New(10, 3, 5, 0)
	for i := 0; i < 200; i++ {
		ce.Feed([]api.Event{{Key: string(rune('a' + i%6)), TS: int64((i * 37) % 100)}})
	}
	const N = 16
	var wg sync.WaitGroup
	vs := make([]map[string][]api.WindowCount, N)
	ds := make([]int64, N)
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) { defer wg.Done(); vs[g], ds[g] = ce.View(), ce.Dropped() }(g)
	}
	wg.Wait()
	concOK := true
	for g := 1; g < N; g++ {
		if !reflect.DeepEqual(vs[g], vs[0]) || ds[g] != ds[0] {
			concOK = false
		}
	}
	check("并发只读视图逐字段相同", concOK)
	// 9) 对外自检方法：内置序列核验四条不变量。
	check("api.SelfCheck 四条不变量全部通过", api.SelfCheck() == nil)
}
