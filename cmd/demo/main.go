package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/lease"
)

var fails int

func ok(cond bool, msg string) {
	if cond {
		fmt.Println("OK   " + msg)
	} else {
		fmt.Println("FAIL " + msg)
		fails++
	}
}

func main() {
	a := api.New(10)
	// 第三节七步：逐步核验 token/owner/expiry 与结果
	step := func(tag, res string, err, wantErr error, wTok, wExp int, wOwn string) {
		o, k, e, _ := a.Lookup("L")
		ok(errors.Is(err, wantErr) && k == wTok && e == wExp && o == wOwn,
			fmt.Sprintf("%s token=%d owner=%s expiry=%d %s", tag, k, o, e, res))
	}
	var err error
	_, err = a.Acquire("L", "A", 0)
	step("S1", "授权", err, nil, 1, 10, "A")
	step("S2", "续期", a.Renew("L", 1, 5), nil, 1, 15, "A")
	step("S3", "续期", a.Renew("L", 1, 12), nil, 1, 22, "A")
	_, err = a.Acquire("L", "B", 20)
	step("S4", "授权", err, nil, 2, 30, "B")
	step("S5", "拒绝", a.Renew("L", 1, 21), lease.ErrStaleToken, 2, 30, "B")
	step("S6", "拒绝", a.Renew("L", 2, 31), lease.ErrExpired, 2, 30, "B")
	ok(a.Expired("L", 30) && !a.Expired("L", 29), "S7 Expired(30)=true 左闭边界")
	// 四不变量自检：栅栏单调、续期只在存活期、朴素一致、失败不留痕
	ok(api.New(10).SelfCheck() == nil, "SelfCheck 栅栏单调/存活期续期/朴素一致/失败不留痕")
	// 四类可判定错误互不相同，被拒后状态不变
	b := api.New(10)
	b.Acquire("x", "A", 0)
	_, e0 := b.Acquire("", "o", 0)
	errs := []error{e0, b.Renew("ghost", 1, 0), b.Renew("x", 99, 1), b.Renew("x", 1, 10)}
	distinct := true
	for i, ei := range errs {
		for j := i + 1; j < len(errs); j++ {
			if ei == nil || errors.Is(ei, errs[j]) {
				distinct = false
			}
		}
	}
	_, _, xe, _ := b.Lookup("x")
	ok(distinct && xe == 10 && b.Renew("x", 1, 5) == nil, "四类错误可判定互不相同 被拒后状态不变")
	// 大 m 下 ExpiredAll 结果正确（检查数有界由 mgr 白盒测试钉住）+ 并发续期正确
	c := api.New(10)
	for i := 0; i < 10000; i++ {
		c.Acquire(fmt.Sprintf("n%d", i), "o", i)
	}
	bigOK := len(c.ExpiredAll(14)) == 5
	d := api.New(100000)
	var wg sync.WaitGroup
	concOK := true
	var mu sync.Mutex
	for g := 0; g < 8; g++ {
		d.Acquire(fmt.Sprintf("w%d", g), "o", 0)
	}
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for n := 1; n <= 100; n++ {
				if d.Renew(fmt.Sprintf("w%d", g), 1, n) != nil {
					mu.Lock()
					concOK = false
					mu.Unlock()
					return
				}
			}
		}(g)
	}
	wg.Wait()
	_, _, fe, _ := d.Lookup("w3")
	ok(bigOK && concOK && fe == 100100, "ExpiredAll大m正确(扫描有界见mgr测试) 并发续期正确")
	if fails > 0 {
		os.Exit(1)
	}
}
