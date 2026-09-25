// demo：逐条演示写时复制页管理器的正确性，全部 OK 时退出码 0。
package main

import (
	"fmt"
	"os"
	"sync"

	"ontology/api"
)

var failed bool

func ok(name string, cond bool, detail string) {
	mark := "OK"
	if !cond {
		mark = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s %s\n", mark, name, detail)
}

// state 返回各存活 view 的 "refcount[内容]" 紧凑串。
func state(a *api.Manager, vs ...api.View) string {
	s := ""
	for i, v := range vs {
		b := make([]byte, 4)
		for o := range b {
			b[o], _ = a.Read(v, o)
		}
		s += fmt.Sprintf("V%d:%d%v", i, a.RefCount(v), b)
	}
	return s
}

func main() {
	ok("SelfCheck", api.SelfCheck() == nil, "")

	// 第三节八步操作，逐步核验页引用计数与内容。
	a := api.NewManager(4, 8)
	v0, _ := a.Alloc([]byte{0, 0, 0, 0})
	v1, _ := a.Snapshot(v0)
	v2, _ := a.Snapshot(v0)
	s12 := state(a, v0, v1, v2) == "V0:3[0 0 0 0]V1:3[0 0 0 0]V2:3[0 0 0 0]"
	_ = a.Write(v1, 0, 9) // 步3：共享→拷贝
	s3 := state(a, v0, v1, v2) == "V0:2[0 0 0 0]V1:1[9 0 0 0]V2:2[0 0 0 0]"
	r00, _ := a.Read(v0, 0)
	r10, _ := a.Read(v1, 0)
	ok("步1-3 共享+拷贝", s12 && s3 && r00 == 0 && r10 == 9, "拷贝后 V0[0]=0 V1[0]=9")
	_ = a.Write(v0, 0, 5) // 步4：仍共享→再拷贝
	s4 := state(a, v0, v1, v2) == "V0:1[5 0 0 0]V1:1[9 0 0 0]V2:1[0 0 0 0]"
	_ = a.Release(v2) // 步5：页 A 归零释放
	s5 := a.PageCount() == 2 && a.RefCount(v2) == 0
	ok("步4-5 再拷贝+释放", s4 && s5, "A 释放后剩 2 页")
	_ = a.Write(v0, 1, 7) // 步6：独占→原地写
	v3, _ := a.Snapshot(v0)
	c0, _ := a.Read(v0, 1)
	s67 := c0 == 7 && a.RefCount(v0) == 2 && a.RefCount(v3) == 2
	r10b, _ := a.Read(v1, 0)
	r00b, _ := a.Read(v0, 0)
	ok("步6-8 原地写+读", s67 && r10b == 9 && r00b == 5, "V0=[5 7 0 0] V1[0]=9")

	// 引用计数守恒 + 写隔离 + 归零释放。
	b := api.NewManager(4, 4)
	w0, _ := b.Alloc([]byte{1, 1, 1, 1})
	w1, _ := b.Snapshot(w0)
	_ = b.Write(w1, 0, 9)
	iso, _ := b.Read(w0, 0)
	_ = b.Release(w0)
	_ = b.Release(w1)
	ok("守恒+隔离+归零释放", iso == 1 && b.PageCount() == 0, "")

	// 四类可判定错误互不相同，被拒后状态不变。
	c := api.NewManager(4, 1)
	x0, _ := c.Alloc([]byte{7, 7, 7, 7})
	_, _ = c.Snapshot(x0)
	e1 := c.Write(api.View(99), 0, 1)
	e2 := c.Write(x0, 9, 1)
	_, e3 := c.Alloc([]byte{1})
	e4 := c.Write(x0, 0, 1) // 拷贝会超 maxPages=1
	distinct := e1 != e2 && e2 != e3 && e3 != e4 && e1 != e3 && e1 != e4 && e2 != e4
	allErr := e1 != nil && e2 != nil && e3 != nil && e4 != nil
	gx, _ := c.Read(x0, 0)
	ok("四类错误+失败不留痕", distinct && allErr && gx == 7 && c.PageCount() == 1 && c.RefCount(x0) == 2, "")

	// 大 m 下写路径 O(1)（检查记录数不随 m 增长，由 mgmt 白盒测试钉住）。
	d := api.NewManager(4, 10002)
	y0, _ := d.Alloc([]byte{0, 0, 0, 0})
	for i := 0; i < 10000; i++ {
		_, _ = d.Snapshot(y0)
	}
	bigm := d.Write(y0, 0, 1) == nil && d.RefCount(y0) == 1
	ok("大m=10000 写仍O(1)", bigm, "每页一条引用计数")

	// 并发：64 goroutine 各自 Snapshot 再写不同偏移，互不影响。
	e := api.NewManager(64, 65)
	z0, _ := e.Alloc(make([]byte, 64))
	zv := make([]api.View, 64)
	var wg sync.WaitGroup
	for i := range zv {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, err := e.Snapshot(z0)
			if err == nil {
				zv[i] = v
				_ = e.Write(v, i, byte(i+1))
			}
		}(i)
	}
	wg.Wait()
	conc := true
	for i, v := range zv {
		for off := 0; off < 64; off++ {
			got, _ := e.Read(v, off)
			want := byte(0)
			if off == i {
				want = byte(i + 1)
			}
			if got != want {
				conc = false
			}
		}
		_ = e.Release(v)
	}
	_ = e.Release(z0)
	ok("并发写互不影响+无泄漏", conc && e.PageCount() == 0, "")

	if failed {
		os.Exit(1)
	}
}
