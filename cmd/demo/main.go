package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/delta"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK", name)
}

func main() {
	var l delta.Log
	l.Append(10)
	l.Append(-4)
	l.Append(0)
	sumOK := l.Value() == 6 && l.Len() == 3
	l.Merge()
	check("delta: 负/零 delta 求和、合并保值", sumOK && l.Value() == 6 && l.Len() == 0)

	d, _ := api.New(16)
	type step struct {
		getA, getB, cnt int64
	}
	var got []step
	rec := func() { got = append(got, step{d.Get("a"), d.Get("b"), int64(d.DeltaCount())}) }
	_ = d.Apply("a", 10)
	_ = d.Apply("b", 20)
	_ = d.Apply("a", -4)
	rec() // 4: Get(a)=6
	_ = d.Compact()
	preA, preB := d.Get("a"), d.Get("b")
	_ = d.Apply("a", 7)
	_ = d.Apply("b", -5)
	rec() // 8: Get(b)=15
	_ = d.Compact()
	rec() // 10: Get(a)=13 Get(b)=15 且第 5 步 Compact 前后一致
	seqOK := got[0] == (step{6, 20, 3}) && got[1] == (step{13, 15, 2}) &&
		got[2] == (step{13, 15, 0}) && preA == 6 && preB == 20
	check("十步序列 Get/DeltaCount 与两次 Compact 保值", seqOK)

	e1, e2 := error(nil), error(nil)
	_, e1 = api.New(0)
	e2 = d.Apply("", 1)
	full, _ := api.New(1)
	_ = full.Apply("x", 1)
	e3 := full.Apply("y", 1)
	check("三类可判定错误互不相同",
		errors.Is(e1, api.ErrNonPositiveMaxDeltas) && errors.Is(e2, api.ErrEmptyKey) &&
			errors.Is(e3, api.ErrTooManyDeltas) && e1 != e2 && e2 != e3 && e1 != e3)

	cnt, va := d.DeltaCount(), d.Get("a")
	_ = d.Apply("", 9)
	check("被拒后状态不变且可继续用", d.DeltaCount() == cnt && d.Get("a") == va && d.Apply("a", 1) == nil)

	big, _ := api.New(20000)
	for m := 100; m <= 10000; m *= 10 {
		for i := 0; i < m; i++ {
			_ = big.Apply("k", 1)
		}
	}
	check("大 m 下 Apply 为纯尾部追加(访问数见 store 测试)", big.Get("k") == 11100)

	want := d.Get("a")
	var wg sync.WaitGroup
	bad := make(chan bool, 1)
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if d.Get("a") != want || d.SelfCheck() != nil {
					select {
					case bad <- true:
					default:
					}
				}
			}
		}()
	}
	wg.Wait()
	check("并发只读结果逐字段一致", len(bad) == 0)

	if failed {
		os.Exit(1)
	}
}
