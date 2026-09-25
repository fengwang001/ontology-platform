package ttl

import (
	"fmt"
	"sync"
	"testing"
)

// TestSweepCheckedSublinear 钉住复杂度约束：m 个全新鲜 key 时 Sweep 检查个数
// 不随 m 线性增长（最小堆peek到队首未过期即停），checked 为与 m 无关的小常数。
func TestSweepCheckedSublinear(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s := New(10)
		for i := 0; i < m; i++ {
			if err := s.Set(fmt.Sprintf("k%d", i), "v", 1000); err != nil {
				t.Fatal(err)
			}
		}
		deleted, err := s.Sweep(1001)
		if err != nil || deleted != 0 {
			t.Fatalf("m=%d: deleted=%d err=%v", m, deleted, err)
		}
		if s.checked > 2 { // 只 peek 堆顶一次即停
			t.Fatalf("m=%d: checked=%d, 应是不随 m 增长的小常数", m, s.checked)
		}
	}
}

// TestSweepDeletesExpired 表驱动：Sweep 删除所有过期 key 并返回个数。
func TestSweepDeletesExpired(t *testing.T) {
	cases := []struct {
		name      string
		tss       []int64
		now       int64
		wantDel   int
		wantAlive int
	}{
		{"全部过期", []int64{0, 1, 2}, 20, 3, 0},
		{"全部新鲜", []int64{15, 16, 17}, 20, 0, 3},
		{"部分过期", []int64{0, 9, 15}, 20, 2, 1},
		{"边界等于过期", []int64{10}, 20, 1, 0},
		{"边界差一不过期", []int64{11}, 20, 0, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := New(10)
			for i, ts := range c.tss {
				if err := s.Set(fmt.Sprintf("k%d", i), "v", ts); err != nil {
					t.Fatal(err)
				}
			}
			del, err := s.Sweep(c.now)
			if err != nil || del != c.wantDel {
				t.Fatalf("del=%d err=%v, want %d", del, err, c.wantDel)
			}
			v, err := s.View(c.now)
			if err != nil || len(v) != c.wantAlive {
				t.Fatalf("alive=%d err=%v, want %d", len(v), err, c.wantAlive)
			}
		})
	}
	// 覆盖写留下陈旧堆项：Sweep 跳过它，不重复删除。
	s := New(10)
	_ = s.Set("k", "old", 0)
	_ = s.Set("k", "new", 5)
	if del, _ := s.Sweep(20); del != 1 {
		t.Fatalf("覆盖写后 Sweep 应删 1 个, 实删 %d", del)
	}
}

// TestClockMonotonic 时钟回退被拒且不改变时钟上界与任何状态。
func TestClockMonotonic(t *testing.T) {
	s := New(10)
	if err := s.Set("a", "1", 100); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("b", "2", 99); err != ErrBackwardClock {
		t.Fatalf("Set 回退: %v", err)
	}
	if _, err := s.Sweep(99); err != ErrBackwardClock {
		t.Fatalf("Sweep 回退: %v", err)
	}
	if _, err := s.View(99); err != ErrBackwardClock {
		t.Fatalf("View 回退: %v", err)
	}
	if _, ok := s.Get("a", 99); ok { // Get 回退被拒：不命中、不删除
		t.Fatal("Get 回退应返回 ok=false")
	}
	v, err := s.View(100) // 上界未被拒绝操作改变，仍为 100
	if err != nil || len(v) != 1 || v["a"] != "1" {
		t.Fatalf("被拒操作改变了状态: %v %v", v, err)
	}
	if _, ok := s.Get("a", 100); !ok { // a 未被回退的 Get 删除
		t.Fatal("回退的 Get 不应删除 key")
	}
}

// TestGetLazyExpire Get 对过期 key 立即删除（含等于）、不刷新 Ts。
func TestGetLazyExpire(t *testing.T) {
	s := New(10)
	_ = s.Set("k", "v", 5)
	if _, ok := s.Get("k", 14); !ok { // 14−5=9<10 命中
		t.Fatal("未过期应命中")
	}
	if _, ok := s.Get("k", 15); ok { // 15−5=10≥10 过期即删
		t.Fatal("等于 TTL 应过期")
	}
	if v, _ := s.View(15); len(v) != 0 {
		t.Fatalf("过期 key 残留: %v", v)
	}
	s2 := New(10) // Get 不刷新 Ts：多次访问后仍按写入时刻过期
	_ = s2.Set("k", "v", 5)
	_, _ = s2.Get("k", 8)
	_, _ = s2.Get("k", 9)
	if _, ok := s2.Get("k", 15); ok {
		t.Fatal("Get 不得刷新 Ts")
	}
}

// TestConcurrent N 写 M 读并发（同一 now），结束后 View 逐 Key 正确。
func TestConcurrent(t *testing.T) {
	s := New(10)
	const n = 128
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(2 * n)
	for i := 0; i < n; i++ {
		go func(i int) { defer wg.Done(); <-start; _ = s.Set(fmt.Sprintf("k%d", i), "v", 1000) }(i)
		go func() { defer wg.Done(); <-start; _, _ = s.Get("k0", 1000); _, _ = s.View(1000) }()
	}
	close(start)
	wg.Wait()
	v, err := s.View(1000)
	if err != nil || len(v) != n {
		t.Fatalf("View: len=%d err=%v, want %d", len(v), err, n)
	}
}
