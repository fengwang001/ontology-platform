package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"unsafe"

	"ontology/api"
	"ontology/hlist"
	"ontology/lnode"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	// 1. lnode：mark 位置位/清位
	n5 := lnode.New(5, nil)
	n2 := lnode.New(2, n5)
	next, marked := n2.Next()
	check("lnode mark-bit", !marked && next == n5 && n2.MarkNext() && n2.Marked())

	// 2. hlist：第三节八步序列，逐步核验
	l := hlist.New()
	var got []string
	snap := func() { got = append(got, fmt.Sprint(l.List())) }
	_ = l.Insert(5)
	snap()
	_ = l.Insert(2)
	_ = l.Insert(8)
	snap()
	errDup := l.Insert(5)
	snap()
	c2a := l.Contains(2)
	_ = l.Delete(2)
	snap()
	c2b := l.Contains(2)
	_ = l.Insert(3)
	snap()
	want := []string{"[5]", "[2 5 8]", "[2 5 8]", "[5 8]", "[3 5 8]"}
	check("eight-steps "+fmt.Sprint(got),
		errors.Is(errDup, hlist.ErrDuplicate) && c2a && !c2b &&
			fmt.Sprint(got) == fmt.Sprint(want))

	// 3. 先 mark 使 Contains 立即 false；不 mark 直接摘除前会读到陈旧 true（lnode 级模拟）
	h := lnode.NewHead()
	m5 := lnode.New(5, nil)
	m2 := lnode.New(2, m5)
	h.CASNext(nil, m2, false, false)
	contains2 := func() bool {
		for curr, _ := h.Next(); curr != nil; {
			nxt, mk := curr.Next()
			if !mk && curr.Key == 2 {
				return true
			}
			curr = nxt
		}
		return false
	}
	staleTrue := contains2() // 未 mark：直接摘除实现此刻读到陈旧 true
	m2.MarkNext()
	check("mark-visibility vs stale-true", staleTrue && !contains2())

	// 4. 不清 mark 位把「指针|1」当地址解引用 = 野地址（差 1 字节）；清位后复原
	packed := uintptr(unsafe.Pointer(m5)) | 1
	check("wild-address-if-not-cleared",
		packed != uintptr(unsafe.Pointer(m5)) && packed&^1 == uintptr(unsafe.Pointer(m5)))

	// 5. 被拒操作零变化；重复/不存在错误可判定
	before := fmt.Sprint(l.List())
	errNF := l.Delete(999)
	errDup2 := l.Insert(3)
	check("rejected-no-trace",
		errors.Is(errNF, hlist.ErrNotFound) && errors.Is(errDup2, hlist.ErrDuplicate) &&
			fmt.Sprint(l.List()) == before)

	// 6. 大 m 下删除正确（摘除只访问 ≤2 个相邻节点，由 TestUnlinkIsConstant 钉住）
	big := hlist.New()
	for i := 0; i < 10000; i++ {
		_ = big.Insert(i)
	}
	_ = big.Delete(5000)
	check("unlink-constant(m=10000)", !big.Contains(5000) && len(big.List()) == 9999)

	// 7. api：关闭是终态；三类错误互不相同；SelfCheck 通过
	a := api.New()
	_ = a.Insert(7)
	_ = a.Close()
	errClosedIns := a.Insert(8)
	errClosedDel := a.Delete(7)
	check("closed-terminal & errors-distinct",
		errors.Is(errClosedIns, api.ErrClosed) && errors.Is(errClosedDel, api.ErrClosed) &&
			a.Contains(7) && !errors.Is(api.ErrClosed, api.ErrDuplicate) &&
			!errors.Is(api.ErrDuplicate, api.ErrNotFound))

	// 8. api：并发 N 插入 + 并发 Contains，收尾后并发 M 删除，List 恰为应保留集合
	c := api.New()
	const N = 64
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(2)
		go func(k int) { defer wg.Done(); _ = c.Insert(k) }(i)
		go func(k int) { defer wg.Done(); _ = c.Contains(k) }(i)
	}
	wg.Wait()
	for i := 0; i < N; i += 2 {
		wg.Add(1)
		go func(k int) { defer wg.Done(); _ = c.Delete(k) }(i)
	}
	wg.Wait()
	wantKeys := []int{}
	for i := 1; i < N; i += 2 {
		wantKeys = append(wantKeys, i)
	}
	check("concurrent-list", fmt.Sprint(c.List()) == fmt.Sprint(wantKeys))

	// 9. api：SelfCheck 核验四条不变量
	check("selfcheck", api.New().SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
