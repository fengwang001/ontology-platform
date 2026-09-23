// demo 逐项演示并判定带租期分布式序列号分配器的全部关键性质。
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"ontology/alloc"
	"ontology/audit"
	"ontology/clock"
	"ontology/durable"
	"ontology/lease"
)

var (
	t0     = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	failed int
)

func judge(name string, ok bool, detail string) {
	tag := "OK"
	if !ok {
		tag = "FAIL"
		failed++
	}
	fmt.Printf("%s %s %s\n", tag, name, detail)
}
func mustCtr(dir string, start uint64) *durable.Counter {
	os.MkdirAll(dir, 0o755)
	c, err := durable.Open(filepath.Join(dir, "counter"), start)
	if err != nil {
		fmt.Println("FAIL 打开持久计数器:", err)
		os.Exit(1)
	}
	return c
}

func leaseAvg(h []lease.Lease) float64 {
	var s uint64
	for _, l := range h {
		s += l.Length
	}
	return float64(s) / float64(len(h))
}
func main() {
	dir, err := os.MkdirTemp("", "seqdemo")
	if err != nil {
		fmt.Println("FAIL 创建临时目录:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)

	d1 := filepath.Join(dir, "c1") // 1. 崩溃后首号 >= 已租段末尾
	a1, _ := alloc.New(mustCtr(d1, 100), clock.NewFake(t0), time.Minute, 100, 100)
	for i := 0; i < 50; i++ {
		a1.Alloc() // 租 [100,200)，发到 150 崩溃
	}
	b1, _ := alloc.New(mustCtr(d1, 100), clock.NewFake(t0), time.Minute, 100, 100)
	first, err := b1.Alloc()
	judge("崩溃后首号>=已租段末尾(200)", err == nil && first >= 200, fmt.Sprintf("首号=%d", first))

	clk2 := clock.NewFake(t0) // 2. 旧段剩余号永不出现
	a2, _ := alloc.New(mustCtr(filepath.Join(dir, "c2"), 0), clk2, 10*time.Second, 10, 10)
	seen := map[uint64]bool{}
	for i := 0; i < 3; i++ {
		v, _ := a2.Alloc()
		seen[v] = true
	}
	clk2.Advance(11 * time.Second)
	reused := false
	for i := 0; i < 5; i++ {
		v, err := a2.Alloc()
		if err != nil || v < 10 {
			reused = true
		}
		seen[v] = true
	}
	for old := uint64(3); old < 10; old++ { // 旧段剩余
		if seen[old] {
			reused = true
		}
	}
	judge("旧段剩余号(3..9)永不出现", !reused, "")

	clk3 := clock.NewFake(t0) // 3. 时钟回拨被拒
	a3, _ := alloc.New(mustCtr(filepath.Join(dir, "c3"), 0), clk3, 10*time.Second, 4, 4)
	a3.Alloc()
	clk3.Advance(-time.Hour)
	_, err = a3.Alloc()
	judge("时钟回拨被拒绝", err == alloc.ErrClockBackwards, fmt.Sprintf("err=%v", err))

	a4, _ := alloc.New(mustCtr(filepath.Join(dir, "c4"), 0), clock.NewFake(t0), time.Hour, 1, 1000) // 4. 写次数
	for i := 0; i < 100000; i++ {
		a4.Alloc()
	}
	judge("10万分发持久写<=120", a4.PersistWrites() <= 120, fmt.Sprintf("实际=%d", a4.PersistWrites()))

	clk5 := clock.NewFake(t0) // 5. 高低频段长差异
	a5, _ := alloc.New(mustCtr(filepath.Join(dir, "c5"), 0), clk5, 10*time.Second, 1, 256)
	for i := 0; i < 2000; i++ {
		a5.Alloc()
	}
	high := leaseAvg(a5.History())
	mark := len(a5.History())
	for i := 0; i < 100; i++ {
		clk5.Advance(11 * time.Second)
		a5.Alloc()
	}
	low := leaseAvg(a5.History()[mark:])
	judge("高频段长显著大于低频", high > 4*low, fmt.Sprintf("高=%.1f 低=%.2f", high, low))

	ctr6 := mustCtr(filepath.Join(dir, "c6"), 0) // 6. 4 实例 10 万号无重复
	streams := make([][]uint64, 4)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a, _ := alloc.New(ctr6, clock.Real{}, time.Minute, 1, 1024)
			for j := 0; j < 25000; j++ {
				if v, err := a.Alloc(); err == nil {
					streams[i] = append(streams[i], v)
				}
			}
		}(i)
	}
	wg.Wait()
	rep := audit.Analyze(streams...)
	judge("4实例10万号无重复且单调", rep.Total == 100000 && rep.Duplicates == 0 && rep.Monotonic,
		fmt.Sprintf("总数=%d 重复=%d", rep.Total, rep.Duplicates))

	clk7 := clock.NewFake(t0) // 7. 空洞长度等式
	d7 := filepath.Join(dir, "c7")
	mk := func() *alloc.Allocator {
		a, _ := alloc.New(mustCtr(d7, 0), clk7, 10*time.Second, 10, 10)
		return a
	}
	aa, sa, sb := mk(), []uint64{}, []uint64{}
	for i := 0; i < 3; i++ {
		v, _ := aa.Alloc()
		sa = append(sa, v)
	}
	clk7.Advance(11 * time.Second) // 作废剩余 7 个
	v, _ := aa.Alloc()
	sa = append(sa, v) // 崩溃丢弃 9 个
	bb := mk()
	for i := 0; i < 4; i++ {
		v, _ := bb.Alloc()
		sb = append(sb, v)
	}
	rep7 := audit.Analyze(sa, sb)
	judge("空洞长度==作废剩余+崩溃丢弃(16)", rep7.GapLength == 16 && rep7.Gaps == 2,
		fmt.Sprintf("空洞=%d段/%d个", rep7.Gaps, rep7.GapLength))

	c8 := mustCtr(filepath.Join(dir, "c8"), 42) // 8. 截断全部拒绝启动
	c8.Next(8)
	full, _ := os.ReadFile(filepath.Join(dir, "c8", "counter"))
	refused := 0
	for cut := 1; cut < len(full); cut++ {
		p := filepath.Join(dir, fmt.Sprintf("t%d", cut), "counter")
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, full[:cut], 0o644)
		if _, err := durable.Open(p, 0); err != nil {
			refused++
		}
	}
	judge("15个截断点全部拒绝启动", refused == len(full)-1, fmt.Sprintf("拒绝=%d/15", refused))

	c9 := mustCtr(filepath.Join(dir, "c9"), 0) // 9. 并发租用无重叠
	var starts [8]uint64
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, _ := c9.Next(10)
			starts[i] = s
		}(i)
	}
	wg.Wait()
	used, overlap := map[uint64]bool{}, false
	for _, s := range starts {
		for v := s; v < s+10; v++ {
			if used[v] {
				overlap = true
			}
			used[v] = true
		}
	}
	judge("8协程并发租用无重叠", !overlap && len(used) == 80, fmt.Sprintf("覆盖=%d", len(used)))

	fmt.Printf("总计: 9 项判定, %d 项失败\n", failed)
	if failed > 0 {
		os.Exit(1)
	}
}
