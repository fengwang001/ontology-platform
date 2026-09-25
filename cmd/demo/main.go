// demo 逐项演示伙伴分配器的正确性，全部 OK 时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sync"

	"ontology/api"
	"ontology/buddy"
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

func eq(got []api.Block, want ...[2]int) bool {
	if len(got) != len(want) {
		return false
	}
	for i, w := range want {
		if got[i].Off != w[0] || got[i].Size != w[1] {
			return false
		}
	}
	return true
}

func main() {
	// buddy 包：分裂取最小偏移、真伙伴级联合并。
	fl := buddy.New(5)
	o1, _, _ := fl.Take(2)
	fl.Release(o1, 2)
	bs := fl.Blocks()
	check("buddy 分裂/级联合并回 [0,32)", len(bs) == 1 && bs[0].Off == 0 && bs[0].Size == 32)

	// 第三节八步表：每步的分配结果与空闲列表。
	a := api.New(5)
	var offs [4]int
	ok := true
	want := [8][][2]int{
		{{4, 4}, {8, 8}, {16, 16}}, {{8, 8}, {16, 16}},
		{{12, 4}, {16, 16}}, {{16, 16}},
		{{4, 4}, {16, 16}}, {{4, 4}, {8, 4}, {16, 16}},
		{{4, 4}, {8, 8}, {16, 16}}, {{0, 32}},
	}
	for i := 0; i < 4; i++ {
		offs[i], _ = a.Alloc(4)
		ok = ok && offs[i] == 4*i && eq(a.FreeList(), want[i]...)
	}
	var listAfter6 []api.Block
	for i, off := range []int{4, 8, 12, 0} {
		ok = ok && a.Free(off) == nil && eq(a.FreeList(), want[4+i]...)
		if i == 1 {
			listAfter6 = a.FreeList()
		}
	}
	check("八步表 0/4/8/12 与每步空闲列表", ok)
	check("第6步 (4,4)与(8,4) 相邻等大但不是伙伴、未合并",
		eq(listAfter6, [2]int{4, 4}, [2]int{8, 4}, [2]int{16, 16}))
	check("Alloc(4) 取 4 而非 8（前四块各占 4 字节）", offs == [4]int{0, 4, 8, 12})

	// 不变量：随机交错操作后 SelfCheck 通过；全释放回单块。
	b := api.New(8)
	r := rand.New(rand.NewSource(1))
	var held []int
	good := true
	for i := 0; i < 2000; i++ {
		if len(held) > 0 && r.Intn(2) == 0 {
			j := r.Intn(len(held))
			good = good && b.Free(held[j]) == nil
			held = append(held[:j], held[j+1:]...)
		} else if off, err := b.Alloc(1 + r.Intn(64)); err == nil {
			held = append(held, off)
		}
		good = good && b.SelfCheck() == nil
	}
	for _, off := range held {
		good = good && b.Free(off) == nil
	}
	check("不变量：守恒/结构合法/全释放回 [0,256)", good && eq(b.FreeList(), [2]int{0, 256}))

	// 三类可判定错误互不相同；被拒后状态不变、仍可使用。
	c := api.New(5)
	full, _ := c.Alloc(32)
	_, errFull := c.Alloc(1)
	_, errZ := c.Alloc(0)
	_, errBig := c.Alloc(33)
	errBad := c.Free(3)
	errDup := func() error { c.Free(full); return c.Free(full) }()
	ok = errors.Is(errZ, api.ErrBadSize) && errors.Is(errBig, api.ErrBadSize) &&
		errors.Is(errFull, api.ErrFull) &&
		errors.Is(errBad, api.ErrBadFree) && errors.Is(errDup, api.ErrBadFree) &&
		!errors.Is(errFull, api.ErrBadFree) && !errors.Is(errBad, api.ErrBadSize)
	check("三类错误可判定且互不相同", ok)
	d := api.New(5)
	snap := fmt.Sprint(d.FreeList())
	d.Alloc(0)
	d.Free(7)
	d.Alloc(64)
	same := fmt.Sprint(d.FreeList()) == snap
	_, err := d.Alloc(32)
	check("被拒后状态不变且仍可正常使用", same && err == nil)

	// 大 m：N=20，m=10000 次 Alloc(1)，释放一半后再分配（检查条数断言见 pool 测试）。
	e := api.New(20)
	mine := make([]int, 0, 10000)
	for off, err := e.Alloc(1); err == nil && len(mine) < 10000; off, err = e.Alloc(1) {
		mine = append(mine, off)
	}
	for i := 0; i < len(mine); i += 2 {
		e.Free(mine[i])
	}
	_, err = e.Alloc(1)
	bigOK := len(mine) == 10000 && err == nil && e.SelfCheck() == nil
	check("大 m 下分配/归还正常（检查条数不随 m 增长，见 TestChecks*）", bigOK)

	// 并发：64 goroutine 各 Alloc(1) 再 Free，结束后回到 [0,1024)。
	f := api.New(10)
	var wg sync.WaitGroup
	gate := make(chan struct{})
	offs2 := make([]int, 64)
	for i := range offs2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-gate
			off, err := f.Alloc(1)
			if err == nil {
				offs2[i] = off
				f.Free(off)
			}
		}(i)
	}
	close(gate)
	wg.Wait()
	check("并发 Alloc/Free 后守恒回 [0,1024)", eq(f.FreeList(), [2]int{0, 1024}))
	if failed {
		os.Exit(1)
	}
}
