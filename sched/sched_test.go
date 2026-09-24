package sched

import (
	"math"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/task"
)

func ceilLog2(n int) int {
	k := 0
	for (1 << k) < n {
		k++
	}
	return k
}

// 同一张表跑完：权重比例、空闲抬升、并列、比较次数、零代价。
func TestScheduler(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "weight ratio 1:3 converges",
			run: func(t *testing.T) {
				s := New()
				if err := s.AddTenant("a", 1); err != nil {
					t.Fatal(err)
				}
				if err := s.AddTenant("b", 3); err != nil {
					t.Fatal(err)
				}
				const n = 30000
				for i := 0; i < n; i++ {
					s.Submit(task.Task{Tenant: "a", Cost: 1, Seq: int64(i)})
					s.Submit(task.Task{Tenant: "b", Cost: 1, Seq: int64(i)})
				}
				count := map[string]int{}
				for range 2 * n {
					j, ok := s.Next()
					if !ok {
						t.Fatal("expected task")
					}
					count[j.Tenant]++
				}
				got := float64(count["a"]) / float64(count["b"])
				if math.Abs(got-1.0/3.0) > (1.0/3.0)*0.05 { // ±5%
					t.Fatalf("ratio a/b=%v want 0.333", got)
				}
			},
		},
		{
			name: "idle tenant rejoins without monopoly",
			run: func(t *testing.T) {
				s := New()
				if err := s.AddTenant("X", 1); err != nil {
					t.Fatal(err)
				}
				if err := s.AddTenant("Y", 1); err != nil {
					t.Fatal(err)
				}
				s.Submit(task.Task{Tenant: "X", Cost: 1, Seq: 0})
				if j, ok := s.Next(); !ok || j.Tenant != "X" {
					t.Fatal("prime X")
				}
				for i := 0; i < 1000; i++ {
					s.Submit(task.Task{Tenant: "Y", Cost: 1, Seq: int64(i)})
					if j, _ := s.Next(); j.Tenant != "Y" {
						t.Fatal("drain Y")
					}
				}
				s.Submit(task.Task{Tenant: "X", Cost: 1, Seq: 1})
				first, _ := s.Next()
				if first.Tenant != "X" {
					t.Fatalf("X must run within a few steps, got %s", first.Tenant)
				}
				// X 此前只有 1 个任务。重新交替提交，Y 必须被公平服务。
				for i := 0; i < 6; i++ {
					s.Submit(task.Task{Tenant: "X", Cost: 1, Seq: int64(10 + i)})
					s.Submit(task.Task{Tenant: "Y", Cost: 1, Seq: int64(1000 + i)})
				}
				seq := []string{}
				for range 12 {
					j, ok := s.Next()
					if !ok {
						t.Fatal("expected more")
					}
					seq = append(seq, j.Tenant)
				}
				yRun, xRun := false, false
				for _, id := range seq {
					if id == "Y" {
						yRun = true
					}
					if id == "X" {
						xRun = true
					}
				}
				if !yRun || !xRun {
					t.Fatalf("expected both tenants served, seq=%v", seq)
				}
			},
		},
		{
			name: "ties broken by tenant ID",
			run: func(t *testing.T) {
				s := New()
				for _, id := range []string{"c", "a", "b"} {
					if err := s.AddTenant(id, 1); err != nil {
						t.Fatal(err)
					}
					s.Submit(task.Task{Tenant: id, Cost: 1, Seq: 0})
				}
				want := []string{"a", "b", "c"}
				for i, w := range want {
					j, _ := s.Next()
					if j.Tenant != w {
						t.Fatalf("step %d got %s want %s", i, j.Tenant, w)
					}
				}
			},
		},
		{
			name: "single selection comparison bound",
			run: func(t *testing.T) {
				s := New()
				const n = 1000
				for i := 0; i < n; i++ {
					id := "t" + string(rune('a'+i/26)) + string(rune('a'+i%26))
					if err := s.AddTenant(id, 1); err != nil {
						t.Fatal(err)
					}
					s.Submit(task.Task{Tenant: id, Cost: 1, Seq: 0})
				}
				if s.Active() != n {
					t.Fatalf("active=%d want %d", s.Active(), n)
				}
				bound := 4 * ceilLog2(n)
				if _, ok := s.Next(); !ok {
					t.Fatal("next")
				}
				if c := s.LastCmp(); c > bound {
					t.Fatalf("comparisons=%d > bound %d", c, bound)
				}
			},
		},
		{
			name: "idle tenants cost no selection slots",
			run: func(t *testing.T) {
				s := New()
				for _, id := range []string{"a", "b", "c", "d"} {
					if err := s.AddTenant(id, 1); err != nil {
						t.Fatal(err)
					}
				}
				s.Submit(task.Task{Tenant: "a", Cost: 1, Seq: 0})
				s.Submit(task.Task{Tenant: "b", Cost: 1, Seq: 0})
				if s.Active() != 2 || s.Len() != 4 {
					t.Fatalf("active=%d len=%d", s.Active(), s.Len())
				}
			},
		},
		{
			name: "zero cost does not monopolize",
			run: func(t *testing.T) {
				s := New()
				if err := s.AddTenant("a", 1); err != nil {
					t.Fatal(err)
				}
				if err := s.AddTenant("b", 1); err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 100; i++ {
					s.Submit(task.Task{Tenant: "a", Cost: 0, Seq: int64(i)})
				}
				s.Submit(task.Task{Tenant: "b", Cost: 1, Seq: 0})
				bSeen := false
				for range 101 {
					j, _ := s.Next()
					if j.Tenant == "b" {
						bSeen = true
					}
				}
				if !bSeen {
					t.Fatal("zero-cost tenant monopolized scheduler")
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}

// 10 万任务并发提交、8 消费者：每个任务恰好被取出一次。
func TestConcurrentExactlyOnce(t *testing.T) {
	s := New()
	const tenants = 10
	for i := 0; i < tenants; i++ {
		if err := s.AddTenant(string(rune('a'+i)), 1); err != nil {
			t.Fatal(err)
		}
	}
	const perTenant = 10000
	var wg sync.WaitGroup
	for i := 0; i < tenants; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := string(rune('a' + i))
			for k := 0; k < perTenant; k++ {
				s.Submit(task.Task{Tenant: id, Cost: 1, Seq: int64(k)})
			}
		}(i)
	}

	var mu sync.Mutex
	seen := map[string]map[int64]bool{}
	var got atomic.Int64
	var cwg sync.WaitGroup
	wg.Wait() // 所有任务提交完成后，消费者可在取空时安全退出。
	for c := 0; c < 8; c++ {
		cwg.Add(1)
		go func() {
			defer cwg.Done()
			for {
				j, ok := s.Next()
				if !ok {
					if got.Load() == tenants*perTenant {
						return
					}
					continue
				}
				mu.Lock()
				if seen[j.Tenant] == nil {
					seen[j.Tenant] = map[int64]bool{}
				}
				if seen[j.Tenant][j.Seq] {
					mu.Unlock()
					t.Errorf("duplicate %s/%d", j.Tenant, j.Seq)
					return
				}
				seen[j.Tenant][j.Seq] = true
				total := got.Add(1)
				mu.Unlock()
				if total == tenants*perTenant {
					return
				}
			}
		}()
	}
	cwg.Wait()
	if got.Load() != tenants*perTenant {
		t.Fatalf("got=%d want %d", got.Load(), tenants*perTenant)
	}
}
