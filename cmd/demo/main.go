package main

import (
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/ev"
	"ontology/plan"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Println(map[bool]string{true: "OK", false: "FAIL"}[ok], name)
}

func main() {
	one := "1"
	evOK := errors.Is(ev.CheckExpect(ev.Put("k", "v", nil), "o", true), ev.ErrExpectation) &&
		ev.CheckExpect(ev.Put("k", "v", &one), "1", true) == nil &&
		errors.Is(ev.CheckExpect(ev.Del("k", &one), "", false), ev.ErrExpectation)
	check("ev: 期望判定(不存在/等值/键缺失)", evOK)

	// 第三节八事件批，视图 {k1:1,k2:2}
	s1, s2 := "1", "2"
	base := map[string]string{"k1": "1", "k2": "2"}
	lookup := func(k string) (string, bool) { v, ok := base[k]; return v, ok }
	batch := []ev.Event{
		ev.Put("k3", "30", nil), ev.Put("k1", "10", &s1), ev.Del("k2", &s2),
		ev.Put("k2", "20", nil), ev.Put("k4", "40", nil), ev.Del("k3", nil),
		ev.Del("k4", nil), ev.Put("k5", "50", nil),
	}
	var r plan.Rehearser
	_, err4 := r.Rehearse(batch[:4], lookup)
	check("plan: 八事件批第4步(批内删后重建)通过", err4 == nil)
	_, err8 := r.Rehearse(batch, lookup)
	check("plan: 第6步首失败整批拒(错误=event 5)", errors.Is(err8, ev.ErrExpectation) && err8.Error()[:7] == "event 5")

	eng := api.New()
	_ = eng.ApplyBatch([]api.Event{api.Put("k1", "1", nil), api.Put("k2", "2", nil)})
	err := eng.ApplyBatch(batch)
	v := eng.View()
	check("api: 整批被拒后视图仍{k1:1,k2:2}", errors.Is(err, ev.ErrExpectation) && v["k1"] == "1" && v["k2"] == "2" && len(v) == 2)

	sx := "x"
	eng2 := api.New()
	_ = eng2.ApplyBatch([]api.Event{api.Put("k1", "1", nil)})
	okB := eng2.ApplyBatch([]api.Event{api.Put("k1", "x", &s1), api.Put("k1", "y", &sx)})
	check("api: (丙)批内链式期望通过,k1=y", okB == nil && eng2.View()["k1"] == "y")

	e1 := eng2.ApplyBatch(nil)
	e2 := eng2.ApplyBatch([]api.Event{api.Put("", "v", nil)})
	e3 := eng2.ApplyBatch([]api.Event{api.Del("k1", nil)})
	distinct := errors.Is(e1, api.ErrEmptyBatch) && errors.Is(e2, ev.ErrEmptyKey) && errors.Is(e3, ev.ErrExpectation) &&
		!errors.Is(e1, ev.ErrEmptyKey) && !errors.Is(e2, ev.ErrExpectation) && !errors.Is(e3, api.ErrEmptyBatch)
	check("api: 三类哨兵错误互异(空批/空键/前置)", distinct)
	check("api: 失败不留痕且自检通过", eng2.View()["k1"] == "y" && eng2.SelfCheck() == nil)

	lookupOK := true
	for _, m := range []int{100, 1000, 10000} {
		big := map[string]string{}
		for i := 0; i < m; i++ {
			big[strconv.Itoa(i)] = "v"
		}
		calls := 0
		var rr plan.Rehearser
		_, err := rr.Rehearse([]ev.Event{ev.Put("newkey", "v", nil)}, func(k string) (string, bool) {
			calls++
			v, ok := big[k]
			return v, ok
		})
		lookupOK = lookupOK && err == nil && calls <= 2 // 1 条事件 + 小常数，与 m 无关
	}
	check("plan: 查找次数不随m增长(m=100..10000)", lookupOK)

	eng3 := api.New()
	var stop atomic.Bool
	var wg sync.WaitGroup
	boundary := atomic.Bool{}
	boundary.Store(true)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				w := eng3.View()
				if w["ga"] != w["gb"] { // 同批两键代数必须一致
					boundary.Store(false)
				}
			}
		}()
	}
	for n := 0; n < 200; n++ {
		s := strconv.Itoa(n)
		var exp *string
		if n > 0 {
			p := strconv.Itoa(n - 1)
			exp = &p
		}
		_ = eng3.ApplyBatch([]api.Event{api.Put("ga", s, exp), api.Put("gb", s, exp)})
	}
	stop.Store(true)
	wg.Wait()
	check("api: 并发读只见完整批次边界", boundary.Load() && eng3.View()["ga"] == "199")

	if failed {
		fmt.Println("RESULT FAIL")
	} else {
		fmt.Println("RESULT OK")
	}
}
