package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/mvcc"
	"ontology/ver"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Printf("%s: %s\n", name, map[bool]string{true: "OK", false: "FAIL"}[ok])
}

func main() {
	// ver: 待提交集合与最新已提交版本选择
	var c ver.Chain
	_, had := c.Latest()
	c.Commit("a", 1)
	c.Commit("b", 2) // 只保留最新已提交版本
	v, ok := c.Latest()
	p := ver.NewPending()
	p.Set("k", "own")
	pv, pok := p.Get("k")
	check("ver chain/pending", !had && ok && v == "b" && pok && pv == "own")

	// mvcc: 第三节 15 步序列，核对第 8/11/12/14/15 步的读返回
	s := mvcc.New()
	t1 := s.Begin()
	_ = s.Write(t1, "X", "1")
	_ = s.Commit(t1)
	t2 := s.Begin()
	_ = s.Write(t2, "Y", "2")
	_ = s.Commit(t2)
	t3 := s.Begin()
	r8, _ := s.ReadTx(t3, "X")
	t4 := s.Begin()
	_ = s.Write(t4, "X", "5")
	r11, _ := s.ReadTx(t3, "X")
	r12, _ := s.ReadTx(t4, "X")
	_ = s.Commit(t4)
	r14, _ := s.ReadTx(t3, "X")
	r15, _ := s.ReadTx(t3, "Y")
	check("mvcc 15-step reads 1/1/5/5/2",
		r8 == "1" && r11 == "1" && r12 == "5" && r14 == "5" && r15 == "2")
	check("mvcc read cost O(1) m=10000", s.CheckReadCost(10000) == nil)

	// api: 自检覆盖四条不变量
	d := api.New()
	check("api SelfCheck", d.SelfCheck() == nil)

	// api: 未提交不可见（Read 与他人的 ReadTx 都看不到）
	du := api.New()
	ua := du.Begin()
	_ = du.Write(ua, "G", "1")
	ub := du.Begin()
	_, eU1 := du.Read("G")
	_, eU2 := du.ReadTx(ub, "G")
	check("api no dirty read",
		errors.Is(eU1, api.ErrKeyNotFound) && errors.Is(eU2, api.ErrKeyNotFound))

	// api: 自身未提交的写对自己可见
	own, eOwn := du.ReadTx(ua, "G")
	check("api own write visible", eOwn == nil && own == "1")

	// api: 读已提交下的不可重复读（同事务两次读同一 key 看到不同值）
	t0 := d.Begin()
	_ = d.Write(t0, "X", "5")
	_ = d.Commit(t0)
	tx := d.Begin()
	w1, _ := d.ReadTx(tx, "X")
	tw := d.Begin()
	_ = d.Write(tw, "X", "9")
	_ = d.Commit(tw)
	w2, _ := d.ReadTx(tx, "X")
	check("api non-repeatable read", w1 == "5" && w2 == "9")

	// api: 四类故障注入错误互不相同且可判定
	e1 := d.Write(tx, "", "v")
	e2 := d.Write(tx, "k", "")
	e3 := d.Write(1<<30, "k", "v")
	tz := d.Begin()
	_ = d.Commit(tz)
	e4 := d.Write(tz, "k", "v")
	distinct := errors.Is(e1, api.ErrEmptyKey) && errors.Is(e2, api.ErrEmptyValue) &&
		errors.Is(e3, api.ErrTxNotBegun) && errors.Is(e4, api.ErrTxCommitted) &&
		e1 != e2 && e2 != e3 && e3 != e4 && e1 != e3 && e1 != e4 && e2 != e4
	check("api 4 distinct sentinel errors", distinct)

	// api: 被拒后状态不变（已提交值不变、事务仍可用）
	still, _ := d.ReadTx(tx, "X")
	_ = d.Write(tx, "Z", "z")
	_ = d.Commit(tx)
	zv, _ := d.Read("Z")
	check("api state intact after rejections", still == "9" && zv == "z")

	// api: 并发只读同一批 key，逐 key 结果一致
	const n = 64
	want, _ := d.Read("X")
	got := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i], _ = d.Read("X")
		}(i)
	}
	wg.Wait()
	same := true
	for _, g := range got {
		same = same && g == want
	}
	check("api concurrent reads consistent", same)

	if failed {
		os.Exit(1)
	}
}
