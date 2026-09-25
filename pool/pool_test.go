package pool

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// 驱逐时从 LRU 端扫描的条目数不随池规模 m 增长（O(1) 定位）。
func TestEvictionScanConstant(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			p := New(m)
			for i := 0; i < m; i++ {
				if _, err := p.Intern(fmt.Sprintf("s%d", i)); err != nil {
					t.Fatal(err)
				}
			}
			// 唯一计数==0 的条目且恰在 LRU 端。
			if err := p.Release("s0"); err != nil {
				t.Fatal(err)
			}
			p.scanned = 0
			if _, err := p.Intern("new"); err != nil {
				t.Fatal(err)
			}
			if p.scanned != 1 { // 与 m 无关的小常数：LRU 端第一个即命中
				t.Fatalf("m=%d 时驱逐扫描了 %d 个条目", m, p.scanned)
			}
		})
	}
}

// 并发：N 个 goroutine 反复 Intern/Release 同一批字符串，结束后计数与
// 朴素重放一致（全为 0），且任一时刻 Len <= max。不用 sleep 制造时序。
func TestConcurrent(t *testing.T) {
	const max = 16
	p := New(max)
	keys := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	var done atomic.Bool
	mon := make(chan struct{})
	go func() { // 监视任一时刻 Len <= max
		defer close(mon)
		for !done.Load() {
			if p.Len() > max {
				t.Error("Len 超过 maxEntries")
				return
			}
		}
	}()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				k := keys[(g+i)%len(keys)]
				p.Intern(k)
				p.Release(k)
			}
		}(g)
	}
	wg.Wait()
	done.Store(true)
	<-mon
	if p.Len() != len(keys) {
		t.Fatalf("Len=%d want %d", p.Len(), len(keys))
	}
	for _, e := range p.Snapshot() { // 每 key Intern/Release 次数相等，计数应全为 0
		if e.Refs != 0 {
			t.Fatalf("%s 计数=%d want 0", e.Value, e.Refs)
		}
	}
}
