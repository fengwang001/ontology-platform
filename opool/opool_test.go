package opool

import (
	"sync"
	"testing"

	"ontology/blk"
)

// 钉住复杂度不变量：Acquire/Release 只动栈顶，
// 检查过的空闲块记录条数不随空闲规模 m 增长（常数 <= 1）。
// 计数器 checked 是非导出字段，本测试在同包内直接读取，不经任何导出接口。
func TestAcquireConstantChecks(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		p, err := New(m)
		if err != nil {
			t.Fatalf("New(%d): %v", m, err)
		}
		list := make([]*blk.Block, 0, m)
		for i := 0; i < m; i++ {
			b, err := p.Acquire()
			if err != nil {
				t.Fatalf("Acquire: %v", err)
			}
			list = append(list, b)
		}
		for _, b := range list {
			if err := p.Release(b); err != nil {
				t.Fatalf("Release: %v", err)
			}
		}
		if p.Idle() != m {
			t.Fatalf("m=%d: Idle()=%d, want %d", m, p.Idle(), m)
		}
		if _, err := p.Acquire(); err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		if p.checked > 1 {
			t.Fatalf("m=%d: Acquire 检查了 %d 条空闲记录, 应 <= 1", m, p.checked)
		}
		// Release 同样只动栈顶
		b, _ := p.Acquire()
		if err := p.Release(b); err != nil {
			t.Fatalf("Release: %v", err)
		}
		if p.checked > 1 {
			t.Fatalf("m=%d: Release 检查了 %d 条空闲记录, 应 <= 1", m, p.checked)
		}
	}
}

// 不变量3：被驱逐（reclaimed）的块后续 Acquire 绝不再返回。
func TestEviction(t *testing.T) {
	p, _ := New(2)
	var b [5]*blk.Block
	for i := range b {
		b[i], _ = p.Acquire()
	}
	for _, bb := range b {
		_ = p.Release(bb)
	}
	if p.Idle() != 2 || p.Total() != 2 {
		t.Fatalf("Idle/Total=%d/%d, want 2/2", p.Idle(), p.Total())
	}
	evicted := map[*blk.Block]bool{b[2]: true, b[3]: true, b[4]: true}
	for i := 0; i < 10; i++ {
		got, _ := p.Acquire()
		if evicted[got] {
			t.Fatalf("被驱逐的块 %d 再次被返回", got.ID())
		}
		_ = p.Release(got)
	}
}

// 并发：N 个 goroutine 各 Acquire 再 Release，无同块同时占用，守恒，-race 干净。
func TestConcurrentNoDuplicate(t *testing.T) {
	const n = 128
	p, _ := New(n)
	var wg sync.WaitGroup
	var mu sync.Mutex
	held, dup := map[*blk.Block]bool{}, false
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			b, _ := p.Acquire()
			mu.Lock()
			dup = dup || held[b]
			held[b] = true
			mu.Unlock()
			_, _ = p.Idle(), p.Total() // 并发只读调用
			mu.Lock()
			delete(held, b)
			mu.Unlock()
			_ = p.Release(b)
		}()
	}
	close(start)
	wg.Wait()
	if dup {
		t.Fatal("同一块被两个 goroutine 同时持有")
	}
	if p.Idle() != p.Total() || p.Total() < 1 || p.Total() > n {
		t.Fatalf("守恒破坏: Idle=%d Total=%d", p.Idle(), p.Total())
	}
}
