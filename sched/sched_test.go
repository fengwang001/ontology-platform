package sched

import (
	"errors"
	"math"
	"math/bits"
	"sync"
	"testing"
	"time"

	"ontology/task"
	"ontology/tenant"
)

func newTest(bump bool, weights map[string]float64) *Scheduler {
	s := New(WithClock(func() time.Time { return time.Time{} }))
	s.bump = bump
	for id, w := range weights {
		if err := s.AddTenant(id, w); err != nil {
			panic(err)
		}
	}
	return s
}

func drain(s *Scheduler, n int) []task.Task {
	out := make([]task.Task, 0, n)
	for i := 0; i < n; i++ {
		t, ok := s.Next()
		if !ok {
			break
		}
		out = append(out, t)
	}
	return out
}

// TestFairShare：长期执行次数/代价占比收敛到权重占比（相对偏差<5%）。
func TestFairShare(t *testing.T) {
	cases := []struct {
		name    string
		weights map[string]float64
		counts  map[string]int
	}{
		{"ratio 1:3", map[string]float64{"a": 1, "b": 3},
			map[string]int{"a": 3000, "b": 9000}},
		{"ratio 1:3:6 over 100k", map[string]float64{"a": 1, "b": 3, "c": 6},
			map[string]int{"a": 10000, "b": 30000, "c": 60000}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTest(true, tc.weights)
			total := 0
			for id, n := range tc.counts {
				for i := 0; i < n; i++ {
					if _, err := s.Submit(id, 1); err != nil {
						t.Fatal(err)
					}
				}
				total += n
			}
			got := map[string]int{}
			for _, tk := range drain(s, total) {
				got[tk.Tenant]++
			}
			var sumW float64
			for _, w := range tc.weights {
				sumW += w
			}
			for id, w := range tc.weights {
				want := w / sumW
				share := float64(got[id]) / float64(total)
				rel := math.Abs(share-want) / want
				t.Logf("%s share=%.5f want=%.5f relErr=%.4f", id, share, want, rel)
				if rel >= 0.05 {
					t.Fatalf("%s relErr %.4f >= 5%%", id, rel)
				}
			}
		})
	}
}

// TestRejoin：空闲租户重新加入时 vt 抬升与否，X 连续执行步数对比。
func TestRejoin(t *testing.T) {
	cases := []struct {
		name string
		bump bool
		want int
	}{
		{"vt raised", true, 1},
		{"vt not raised", false, 1000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTest(tc.bump, map[string]float64{"X": 1, "Y": 1})
			s.Submit("X", 1)
			s.Next() // X 获得旧 vt=1，随后空闲
			for i := 0; i < 1000; i++ {
				s.Submit("Y", 1)
			}
			drain(s, 1000) // Y 空闲期执行 1000 个
			for i := 0; i < 2000; i++ {
				s.Submit("X", 1)
			}
			s.Submit("Y", 1)
			xRun, yAt := 0, -1
			for i := 0; i < 2002; i++ {
				tk, ok := s.Next()
				if !ok {
					break
				}
				if yAt == -1 && tk.Tenant == "Y" {
					yAt = i + 1
				}
				if yAt == -1 {
					xRun++
				}
			}
			t.Logf("X 连续执行 %d 步，Y 首次出现在第 %d 步", xRun, yAt)
			if xRun != tc.want {
				t.Fatalf("xRun=%d want=%d", xRun, tc.want)
			}
			if tc.bump && yAt > 2 {
				t.Fatalf("Y 被饿死：第 %d 步才执行", yAt)
			}
		})
	}
}

// TestTie：vt 相同按租户 ID 字典序打破并列。
func TestTie(t *testing.T) {
	cases := []struct {
		submit string
		want   string
	}{
		{"z", "z"}, {"a", "a"}, {"m", "m"}, {"z", "a"}, {"a", "m"}, {"m", "z"},
	}
	s := newTest(true, map[string]float64{"a": 1, "m": 1, "z": 1})
	for i, tc := range cases {
		s.Submit(tc.submit, 1)
		if i%3 == 2 {
			tk := drain(s, 3)
			got := tk[0].Tenant + tk[1].Tenant + tk[2].Tenant
			if got != "amz" {
				t.Fatalf("tie order=%s want amz", got)
			}
		}
	}
}

// TestHeapComparisons：1000 租户单次选择比较次数 <= 4*ceil(log2(1000))；
// 堆中元素数等于非空租户数。
func TestHeapComparisons(t *testing.T) {
	w := map[string]float64{}
	for i := 0; i < 1000; i++ {
		w[idOf(i)] = 1
	}
	s := newTest(true, w)
	for i := 0; i < 1000; i++ {
		s.Submit(idOf(i), 1)
		s.Submit(idOf(i), 1)
	}
	bound := 4 * bits.Len(uint(999)) // ceil(log2(1000))=10
	for i := 0; i < 2000; i++ {
		s.Next()
		if c := s.Comparisons(); c > int64(bound) {
			t.Fatalf("comparisons=%d > bound=%d", c, bound)
		}
	}
	small := newTest(true, map[string]float64{"a": 1, "b": 1, "c": 1})
	small.Submit("a", 1)
	small.Submit("c", 1)
	if small.HeapLen() != 2 {
		t.Fatalf("heap len=%d want 2 (non-empty only)", small.HeapLen())
	}
}

func idOf(i int) string {
	return "t" + string(rune('A'+i/26)) + string(rune('A'+i%26)) +
		string(rune('0'+i%10))
}

// TestZeroCost：零代价任务合法且租户不会无限独占。
func TestZeroCost(t *testing.T) {
	s := newTest(true, map[string]float64{"A": 1, "B": 1})
	for i := 0; i < 3000; i++ {
		s.Submit("A", 0)
	}
	for i := 0; i < 3; i++ {
		s.Submit("B", 1)
	}
	got := map[string]int{}
	for _, tk := range drain(s, 3003) {
		got[tk.Tenant]++
	}
	if got["A"] != 3000 || got["B"] != 3 {
		t.Fatalf("counts=%v, zero-cost 租户疑似独占或丢任务", got)
	}
}

// TestErrors：四类边界错误均可用 errors.Is 区分。
func TestErrors(t *testing.T) {
	s := newTest(true, map[string]float64{"a": 1})
	s.Submit("a", 1)
	_, badCost := s.Submit("a", -1)
	_, unknown := s.Submit("nope", 1)
	cases := []struct {
		name string
		err  error
		want error
	}{
		{"bad weight", s.AddTenant("z", 0), tenant.ErrBadWeight},
		{"bad cost", badCost, ErrBadCost},
		{"unknown tenant", unknown, ErrUnknownTenant},
		{"remove active tenant", s.RemoveTenant("a"), ErrTenantActive},
	}
	for _, tc := range cases {
		if !errors.Is(tc.err, tc.want) {
			t.Fatalf("%s: err=%v want %v", tc.name, tc.err, tc.want)
		}
	}
}

// TestConcurrency：10 万任务并发提交、8 消费者，不丢不重。
func TestConcurrency(t *testing.T) {
	s := New()
	for i := 0; i < 10; i++ {
		s.AddTenant(idOf(i), float64(1+i%3))
	}
	var mu sync.Mutex
	seen := map[int64]bool{}
	var wg sync.WaitGroup
	for c := 0; c < 8; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for tk, ok := s.Next(); ok; tk, ok = s.Next() {
				mu.Lock()
				if seen[tk.Seq] {
					t.Errorf("dup seq %d", tk.Seq)
				}
				seen[tk.Seq] = true
				mu.Unlock()
			}
		}()
	}
	const total = 100000
	var submitWG sync.WaitGroup
	for c := 0; c < 8; c++ {
		submitWG.Add(1)
		go func(c int) {
			defer submitWG.Done()
			for i := c; i < total; i += 8 {
				if _, err := s.Submit(idOf(i%10), 1); err != nil {
					t.Error(err)
				}
			}
		}(c)
	}
	submitWG.Wait()
	s.Close()
	wg.Wait()
	if len(seen) != total {
		t.Fatalf("consumed=%d want %d", len(seen), total)
	}
}

// TestDeterminism：固定任务流跑 20 次，调度序列逐元素一致。
func TestDeterminism(t *testing.T) {
	flow := func() []string {
		s := newTest(true, map[string]float64{"a": 1, "b": 3, "c": 6})
		costs := []float64{1, 2, 3}
		ids := []string{"a", "b", "c"}
		for i := 0; i < 600; i++ {
			s.Submit(ids[i%3], costs[i%3])
		}
		seq := make([]string, 600)
		for i, tk := range drain(s, 600) {
			seq[i] = tk.Tenant
		}
		return seq
	}
	base := flow()
	for r := 1; r < 20; r++ {
		got := flow()
		for i := range base {
			if got[i] != base[i] {
				t.Fatalf("run %d pos %d: %s != %s", r, i, got[i], base[i])
			}
		}
	}
}
