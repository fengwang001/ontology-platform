// 演示程序：事件时间去重窗口。不读参数、不联网；逐条打印 OK/FAIL，输出不超过 10 行。
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/dedup"
	"ontology/dwin"
)

var fails int

func line(name string, cond bool) string {
	if !cond {
		fails++
		return "FAIL " + name
	}
	return "OK " + name
}

// run 逐条喂入，返回每步新/重复判定（N/R）与每步水位线。
func run(d *api.Deduper, evs []api.Event) (v []byte, wms []int64) {
	for _, ev := range evs {
		out, e := d.Feed([]api.Event{ev})
		if e != nil {
			fails++
		}
		if len(out) == 1 {
			v = append(v, 'N')
		} else {
			v = append(v, 'R')
		}
		wm, _ := d.Watermark()
		wms = append(wms, wm)
	}
	return
}

// naiveRef 朴素参照：保留全部历史新事件，整表倒序扫该 ID 最近首见 TS，按同一边界判定。
func naiveRef(ttl, delay int64, evs []api.Event) []byte {
	var hID []string
	var hTS []int64
	var res []byte
	var maxTS int64
	for i, ev := range evs {
		if i == 0 || ev.TS > maxTS {
			maxTS = ev.TS
		}
		c := byte('N')
		for j := len(hID) - 1; j >= 0; j-- {
			if hID[j] == ev.ID {
				if maxTS-delay < hTS[j]+ttl {
					c = 'R'
				}
				break
			}
		}
		res = append(res, c)
		if c == 'N' {
			hID, hTS = append(hID, ev.ID), append(hTS, ev.TS)
		}
	}
	return res
}

func main() {
	seq := []api.Event{{ID: "a", TS: 5}, {ID: "b", TS: 8}, {ID: "a", TS: 9}, {ID: "c", TS: 17},
		{ID: "a", TS: 14}, {ID: "b", TS: 20}, {ID: "d", TS: 4}, {ID: "d", TS: 6},
		{ID: "c", TS: 26}, {ID: "a", TS: 25}}
	d, _ := api.New(10, 2, 1000)
	verd, wms := run(d, seq)
	var L []string
	L = append(L, line(fmt.Sprintf("十步判定 %s（水位线 %v）", verd, wms),
		string(verd) == "NNRNNNNNRN" &&
			reflect.DeepEqual(wms, []int64{3, 6, 7, 15, 15, 18, 18, 18, 24, 24})))
	L = append(L, line(fmt.Sprintf("第4步边界清除 a:5=%v；第10步后记忆 %v（dup=%d）",
		dwin.Expired(15, true, 5, 10), d.Mem(), d.Dups()),
		dwin.Expired(15, true, 5, 10) && d.Dups() == 2 &&
			reflect.DeepEqual(d.Mem(), map[string]int64{"a": 25, "b": 20, "c": 17})))
	L = append(L, line("随机序列（含迟到）与朴素参照一致",
		string(naiveRef(10, 2, seq)) == string(verd) && randAgrees()))
	_, e1 := api.New(0, 2, 1) // 三类互异可判定错误；被拒整批不留痕、之后仍可用
	d2, _ := api.New(10, 2, 1)
	_, e2 := d2.Feed([]api.Event{{ID: "", TS: 1}})
	_, e3 := d2.Feed([]api.Event{{ID: "x", TS: 1}, {ID: "y", TS: 2}})
	d2.Feed([]api.Event{{ID: "x", TS: 1}})
	L = append(L, line(fmt.Sprintf("三类错误互异 %v/%v/%v", e1, e2, e3),
		errors.Is(e1, api.ErrInvalidParam) && errors.Is(e2, api.ErrEmptyID) && errors.Is(e3, api.ErrTooMany)))
	L = append(L, line("被拒后状态不变且实例仍可用",
		reflect.DeepEqual(d2.Mem(), map[string]int64{"x": 1}) && d2.Dups() == 0 && len(d2.Emitted()) == 1))
	L = append(L, line("大 m 下探测条数不随 m 增长（无数值外泄）", dedup.CheckProbeBound() == nil))
	L = append(L, line("公开 SelfCheck 四条不变量", d.SelfCheck() == nil))
	wantE, wantM := d.Emitted(), d.Mem() // 16 goroutine 经关闭通道并发只读，无 sleep
	wantWM, wantOK := d.Watermark()
	res := make([]bool, 16)
	barrier := make(chan struct{})
	var wg sync.WaitGroup
	for g := range res {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-barrier
			ok := true
			for k := 0; k < 30; k++ {
				wm, wok := d.Watermark()
				ok = ok && reflect.DeepEqual(d.Emitted(), wantE) && reflect.DeepEqual(d.Mem(), wantM) &&
					d.Dups() == 2 && wm == wantWM && wok == wantOK
			}
			res[g] = ok
		}(g)
	}
	close(barrier)
	wg.Wait()
	allOK := true
	for _, ok := range res {
		allOK = allOK && ok
	}
	L = append(L, line("16 goroutine 并发只读逐字段一致", allOK))
	for _, s := range L {
		fmt.Println(s)
	}
	if fails > 0 {
		os.Exit(1)
	}
}

// randAgrees 跑多组随机序列，逐事件比对实现与朴素参照的新/重复判定。
func randAgrees() bool {
	for trial := 0; trial < 50; trial++ {
		rng := rand.New(rand.NewSource(int64(trial)))
		evs := make([]api.Event, 200)
		ts := int64(50)
		for i := range evs {
			ts += int64(rng.Intn(13)) - 3
			evs[i] = api.Event{ID: fmt.Sprintf("id%d", rng.Intn(8)), TS: ts}
		}
		dd, _ := api.New(10, 2, 1_000_000)
		got, _ := run(dd, evs)
		if string(got) != string(naiveRef(10, 2, evs)) {
			return false
		}
	}
	return true
}
