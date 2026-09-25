package pool

import (
	"sync"
	"testing"
)

// 钉住复杂度：Alloc 按大小分档定位，检查条数不随空闲块总数 m 线性增长。
func TestChecksAlloc(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		p := New(20)
		offs := make([]int, 0, m)
		for i := 0; i < m; i++ {
			off, err := p.Alloc(1)
			if err != nil {
				t.Fatalf("m=%d 第 %d 次 Alloc: %v", m, i, err)
			}
			offs = append(offs, off)
		}
		for i := 0; i < m; i += 2 { // 释放一半，得约 m/2 个大小为 1 的空闲块
			if err := p.Free(offs[i]); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := p.Alloc(1); err != nil {
			t.Fatal(err)
		}
		if p.checks > 3 { // 与 m 无关的小常数：0 阶档直接命中
			t.Fatalf("m=%d Alloc 检查条数 %d，随 m 增长", m, p.checks)
		}
	}
}

// 钉住复杂度：级联合并的检查条数只随 N（对数）增长，与空闲块总数无关。
func TestChecksFree(t *testing.T) {
	for _, run := range []int{10, 15} { // 连续 2^run 个大小为 1 的块
		p := New(20)
		n := 1 << run
		offs := make([]int, 0, n)
		for i := 0; i < n; i++ {
			off, err := p.Alloc(1)
			if err != nil {
				t.Fatal(err)
			}
			offs = append(offs, off)
		}
		// 制造大量环绕的空闲小块：再分配一批并隔一个释放。
		var noise []int
		for i := 0; i < 4000; i++ {
			off, err := p.Alloc(1)
			if err != nil {
				t.Fatal(err)
			}
			noise = append(noise, off)
		}
		for i := 0; i < len(noise); i += 2 {
			if err := p.Free(noise[i]); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < n-1; i++ { // 释放连续区前 n-1 块
			if err := p.Free(offs[i]); err != nil {
				t.Fatal(err)
			}
		}
		if err := p.Free(offs[n-1]); err != nil { // 最后一块触发 run 层级联
			t.Fatal(err)
		}
		if p.checks != run+1 { // 恰好 run 次命中 + 1 次未命中，与 4000 个环绕块无关
			t.Fatalf("run=%d 级联合并检查条数 %d，期望 %d（对数）", run, p.checks, run+1)
		}
	}
}

// 并发：g 个 goroutine 并发各 Alloc(1) 再各自 Free；并发 Free 期间
// 只读采样到的空闲字节数单调不减；全部结束后回到单块 [0,S)。
func TestConcurrency(t *testing.T) {
	p := New(12)
	const g = 64
	offs := make([]int, g)
	for i := range offs { // 先占 g 个大小为 1 的块
		off, err := p.Alloc(1)
		if err != nil {
			t.Fatal(err)
		}
		offs[i] = off
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range offs { // 并发归还各自偏移
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if err := p.Free(offs[i]); err != nil {
				t.Error(err)
			}
		}(i)
	}
	fin := make(chan struct{})
	go func() { wg.Wait(); close(fin) }()
	close(start)
	last := 0
loop:
	for { // 只读采样，不用 sleep 制造时序
		select {
		case <-fin:
			break loop
		default:
		}
		sum := 0
		for _, b := range p.FreeList() {
			sum += b.Size
		}
		if sum < last {
			t.Fatalf("空闲字节数回退 %d -> %d", last, sum)
		}
		last = sum
	}
	if fl := p.FreeList(); len(fl) != 1 || fl[0].Off != 0 || fl[0].Size != 4096 {
		t.Fatalf("并发后空闲列表=%v，应回 [0,4096)", fl)
	}
}
