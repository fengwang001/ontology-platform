package ttl

import (
	"errors"
	"strconv"
	"sync"
	"testing"
)

// TestSweepCheckedBounded 钉住不变量 2 的复杂度面：m 个存活 key（无一过期）时，
// 一次 Sweep 检查（peek/pop）的 key 个数不随 m 线性增长——最小堆查到队首未过期即停。
func TestSweepCheckedBounded(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		s := New(10)
		for i := 0; i < m; i++ {
			if err := s.Set("k"+strconv.Itoa(i), "v", 0); err != nil {
				t.Fatal(err)
			}
		}
		n, err := s.Sweep(5) // 5−0=5<10，无一过期
		if err != nil || n != 0 {
			t.Fatalf("m=%d: Sweep=(%d,%v)", m, n, err)
		}
		if s.checked > 2 { // 与 m 无关的小常数：peek 队首一次即停
			t.Fatalf("m=%d: checked=%d，随 m 增长，疑似整表扫描", m, s.checked)
		}
	}
}

// TestConcurrentReadWrite 并发钉住：N 写不同 key + M 只读（同一足够大的 now）+ 并发 Sweep，
// 结束后 View 逐 Key 正确。不用 sleep 制造时序，-race 下运行。
func TestConcurrentReadWrite(t *testing.T) {
	const writers, readers, per = 8, 4, 50
	s := New(10)
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < per; j++ {
				_ = s.Set("k"+strconv.Itoa(i*per+j), "v"+strconv.Itoa(j), 1000)
			}
		}(i)
	}
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_, _, _ = s.Get("k0", 1000)
				_, _ = s.View(1000)
				_, _ = s.Sweep(1000) // 同 now 下无 key 过期，Sweep 不应误删
			}
		}()
	}
	wg.Wait()
	view, err := s.View(1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(view) != writers*per {
		t.Fatalf("View 应有 %d 个 key，实得 %d", writers*per, len(view))
	}
	for i := 0; i < writers*per; i++ {
		want := "v" + strconv.Itoa(i%per)
		if view["k"+strconv.Itoa(i)] != want {
			t.Fatalf("key k%d: want %s got %s", i, want, view["k"+strconv.Itoa(i)])
		}
	}
}

// TestClockMonotone 钉住不变量 3：所有操作拒绝时钟回退，且拒绝不改变时钟上界。
func TestClockMonotone(t *testing.T) {
	s := New(10)
	if err := s.Set("a", "1", 10); err != nil {
		t.Fatal(err)
	}
	backs := []func() error{
		func() error { return s.Set("b", "2", 9) },
		func() error { _, _, err := s.Get("a", 5); return err },
		func() error { _, err := s.Sweep(9); return err },
		func() error { _, err := s.View(0); return err },
	}
	for i, f := range backs {
		if !errors.Is(f(), ErrBackwardClock) {
			t.Fatalf("case %d: 应返回 ErrBackwardClock", i)
		}
	}
	if err := s.Set("b", "2", 10); err != nil { // 上界仍是 10：等值可用
		t.Fatalf("被拒操作改变了时钟上界: %v", err)
	}
	if _, _, err := s.Get("a", 9); !errors.Is(err, ErrBackwardClock) {
		t.Fatal("上界应仍为 10")
	}
}

// TestRejectLeavesNoTrace 钉住不变量 4：被拒操作不改变任何状态，之后仍可正常使用。
func TestRejectLeavesNoTrace(t *testing.T) {
	s := New(10)
	_ = s.Set("a", "1", 5)
	_ = s.Set("b", "2", 8)
	before, _ := s.View(8)
	rejects := []func() error{
		func() error { return s.Set("", "x", 9) },
		func() error { _, _, err := s.Get("", 9); return err },
		func() error { return s.Set("c", "3", 4) },
		func() error { _, err := s.Sweep(1); return err },
		func() error { _, err := s.View(2); return err },
	}
	for i, f := range rejects {
		if f() == nil {
			t.Fatalf("case %d 应被拒绝", i)
		}
	}
	after, _ := s.View(8)
	if len(after) != len(before) {
		t.Fatalf("key 集合被改: %v -> %v", before, after)
	}
	for k, v := range before {
		if after[k] != v {
			t.Fatalf("%s 的值被改", k)
		}
	}
	if _, ok, _ := s.Get("a", 14); !ok { // a 的 Ts 仍是 5：14−5=9<10 活
		t.Fatal("Ts 被改")
	}
	if _, ok, _ := s.Get("a", 15); ok { // 15−5=10 过期
		t.Fatal("Ts 被改")
	}
}
