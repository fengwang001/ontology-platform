package rr

import (
	"math/rand"
	"sync"
	"testing"
)

// TestBlockAdvance 证明 CPU 按时间片整块推进而非逐 tick 模拟：
// 单进程 burst=m、quantum>=m 时，逐单位推进计数器恒为 0，不随 m 增长。
func TestBlockAdvance(t *testing.T) {
	for _, m := range []int64{100, 500, 1000, 5000, 10000} {
		s, err := New(m) // quantum = m >= burst
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Add(1, 0, m); err != nil {
			t.Fatal(err)
		}
		if got := s.Run(); got[1] != m {
			t.Fatalf("m=%d: completion=%d want %d", m, got[1], m)
		}
		if s.ticks != 0 {
			t.Fatalf("m=%d: ticks=%d, want 0 (block advance)", m, s.ticks)
		}
	}
}

// 不变量 2：每个进程被 CPU 服务的总时长恰等于 burst。
func TestConservation(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 50; i++ { // 单进程独占 CPU：完成时刻 == arrival+burst
		a, b := rng.Int63n(50), 1+rng.Int63n(100)
		s, _ := New(1 + rng.Int63n(20))
		if err := s.Add(1, a, b); err != nil {
			t.Fatal(err)
		}
		if got := s.Run(); got[1] != a+b {
			t.Errorf("arrival=%d burst=%d: completion=%d want %d", a, b, got[1], a+b)
		}
	}
	cases := []struct {
		q  int64
		ps [][3]int64
	}{
		{1, [][3]int64{{1, 0, 3}, {2, 0, 1}, {3, 0, 2}}},
		{100, [][3]int64{{1, 0, 5}, {2, 0, 9}, {3, 0, 1}}},
		{4, [][3]int64{{1, 0, 10}, {2, 0, 4}, {3, 0, 3}, {4, 0, 2}}},
	}
	for _, c := range cases { // 全部同时到达：CPU 不空转，makespan == sum(burst)
		s, _ := New(c.q)
		sum := int64(0)
		for _, p := range c.ps {
			if err := s.Add(p[0], p[1], p[2]); err != nil {
				t.Fatal(err)
			}
			sum += p[2]
		}
		maxCT := int64(0)
		for _, ct := range s.Run() {
			maxCT = max(maxCT, ct)
		}
		if maxCT != sum {
			t.Errorf("q=%d: makespan=%d want sum(burst)=%d", c.q, maxCT, sum)
		}
	}
}

// 并发：N 个 goroutine 各 Add 一个不同 pid，Run 结果与顺序登记的参照完全一致。
func TestConcurrentAdd(t *testing.T) {
	const n = 64
	conc, _ := New(3)
	seq, _ := New(3)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		pid, burst := int64(i+1), int64(1+i%5) // 全在 t=0 到达，结果与 Add 顺序无关
		wg.Add(1)
		go func() { defer wg.Done(); _ = conc.Add(pid, 0, burst) }()
		if err := seq.Add(pid, 0, burst); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	got, want := conc.Run(), seq.Run()
	if len(got) != n {
		t.Fatalf("got %d completions, want %d", len(got), n)
	}
	for pid, ct := range want {
		if got[pid] != ct {
			t.Errorf("pid=%d: got %d want %d", pid, got[pid], ct)
		}
	}
}
