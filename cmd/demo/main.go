package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"slices"
	"strconv"
	"sync"

	"ontology/alm"
	"ontology/api"
	"ontology/thr"
)

func ok(name string, good bool) {
	if !good {
		fmt.Println("FAIL", name)
		os.Exit(1)
	}
	fmt.Println("OK", name)
}

// replay 朴素参照：从 0 起步逐步按规则判定。
func replay(t, h int64, key string, ds []int64) (int64, bool, []api.Event) {
	var v int64
	var on bool
	var evs []api.Event
	for _, d := range ds {
		v += d
		if !on && v >= t {
			on = true
			evs = append(evs, api.Event{Key: key, Type: api.On})
		} else if on && v < t-h {
			on = false
			evs = append(evs, api.Event{Key: key, Type: api.Off})
		}
	}
	return v, on, evs
}

func main() {
	// 1. 第三节八步表（T=10, H=3）
	deltas := []int64{10, 2, -5, 2, -2, 4, -7, 3}
	wantEv := []thr.Event{thr.On, thr.None, thr.None, thr.None, thr.None, thr.None, thr.Off, thr.None}
	wantOn := []bool{true, true, true, true, true, true, false, false}
	var v int64
	var on bool
	good := true
	for i, d := range deltas {
		old := v
		v += d
		n, emit, ev := thr.Judge(old, v, on, 10, 3)
		on = n
		if !emit {
			ev = thr.None
		}
		good = good && ev == wantEv[i] && on == wantOn[i]
	}
	ok("八步事件与最终on", good)

	// 2. 与朴素参照一致
	seq := []int64{10, 2, -5, 2, -2, 4, -7, 3, 25, -30, 25, -1, -18, -1, 12, -20}
	a, err := api.New(10, 3, 100)
	good = err == nil
	for _, d := range seq {
		if _, err = a.Add("k", d); err != nil {
			good = false
		}
	}
	wv, won, wev := replay(10, 3, "k", seq)
	good = good && a.View()["k"] == wv && a.Alarm()["k"] == won && slices.Equal(a.Events(), wev)
	ok("与朴素参照一致", good)

	// 3. 不重复不遗漏：事件类型严格交替且每次翻转恰一条
	evs := a.Events()
	good = len(evs) == len(wev)
	for i, e := range evs {
		want := api.On
		if i%2 == 1 {
			want = api.Off
		}
		good = good && e.Type == want
	}
	ok("报警不重复不遗漏", good)

	// 4. 去抖正确：ON 后在 [T-H, +inf) 抖动无 OFF，破下沿才 OFF
	b, _ := api.New(10, 3, 10)
	good = true
	for _, d := range []int64{10, 5, -5, -3, 2, -1} { // 10→15→10→7→9→8，均 >=7
		if ev, _ := b.Add("d", d); len(ev) != 0 && !(len(ev) == 1 && d == 10) {
			good = false
		}
	}
	ev, _ := b.Add("d", -2) // 8→6 < 7，应 OFF
	good = good && len(ev) == 1 && ev[0].Type == api.Off && len(b.Events()) == 2
	ok("去抖正确", good)

	// 5. 四类可判定错误且互不相同
	_, e1 := api.New(0, 1, 10)
	_, e2 := api.New(10, 10, 10)
	_, e3 := b.Add("", 1)
	c, _ := api.New(10, 3, 10)
	_, _ = c.Add("z", math.MaxInt64)
	_, e4 := c.Add("z", 1)
	es := []error{e1, e2, e3, e4}
	ws := []error{api.ErrBadParam, api.ErrBadHysteresis, api.ErrEmptyKey, api.ErrOverflow}
	good = true
	for i := range es {
		for j := range ws {
			good = good && errors.Is(es[i], ws[j]) == (i == j)
		}
	}
	ok("四类可判定错误", good)

	// 6. 被拒后状态不变
	before := fmt.Sprint(c.View(), c.Alarm(), c.Events())
	_, _ = c.Add("", 1)
	_, _ = c.Add("z", 1)
	_, _ = c.Add("z", math.MaxInt64) // MaxInt64+MaxInt64 溢出，被拒
	ok("被拒后状态不变", fmt.Sprint(c.View(), c.Alarm(), c.Events()) == before)

	// 7. 大 m 下检查个数不随 m 增长
	ok("大m下检查个数不随m增长", alm.ScaleFlat())

	// 8. 并发 Add 结果一致（64 goroutine 各写自己的 Key，并发读不竞态）
	d, _ := api.New(1000, 10, 100)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = d.Add("g"+strconv.Itoa(i), 20)
			}
		}(i)
		wg.Add(1)
		go func() { defer wg.Done(); _ = d.View(); _ = d.Alarm(); _ = d.Events(); _ = d.SelfCheck() }()
	}
	wg.Wait()
	wantView := map[string]int64{}
	wantAlarm := map[string]bool{}
	for i := 0; i < 64; i++ {
		wantView["g"+strconv.Itoa(i)] = 1000
		wantAlarm["g"+strconv.Itoa(i)] = true
	}
	good = reflect.DeepEqual(d.View(), wantView) && reflect.DeepEqual(d.Alarm(), wantAlarm) && len(d.Events()) == 64
	ok("并发Add结果一致", good)

	// 9. SelfCheck
	ok("SelfCheck", d.SelfCheck() == nil)
}
