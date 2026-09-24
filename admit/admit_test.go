package admit_test

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/admit"
	"ontology/sched"
	"ontology/task"
)

func newLimiter(cap int, weights map[string]float64) *admit.Limiter {
	l := admit.New(sched.New(), cap)
	for id, w := range weights {
		if err := l.AddTenant(id, w); err != nil {
			panic(err)
		}
	}
	return l
}

// 四类错误用 errors.Is 区分：非法权重、队列满、有任务时移除、未知租户。
func TestErrors(t *testing.T) {
	full := newLimiter(1, map[string]float64{"a": 1})
	full.Submit(task.Task{Tenant: "a", Cost: 1})
	for _, c := range []struct {
		name string
		err  error
		want error
	}{
		{"零权重", newLimiter(0, nil).AddTenant("x", 0), admit.ErrInvalidWeight},
		{"负权重", newLimiter(0, nil).AddTenant("x", -2), admit.ErrInvalidWeight},
		{"NaN权重", newLimiter(0, nil).AddTenant("x", math.NaN()), admit.ErrInvalidWeight},
		{"Inf权重", newLimiter(0, nil).AddTenant("x", math.Inf(1)), admit.ErrInvalidWeight},
		{"队列满", full.Submit(task.Task{Tenant: "a", Cost: 1}), admit.ErrQueueFull},
		{"有任务时移除", full.RemoveTenant("a"), admit.ErrTenantBusy},
		{"提交到未知租户", full.Submit(task.Task{Tenant: "ghost", Cost: 1}), admit.ErrUnknownTenant},
		{"移除未知租户", full.RemoveTenant("ghost"), admit.ErrUnknownTenant},
	} {
		if !errors.Is(c.err, c.want) {
			t.Errorf("%s: err=%v, want errors.Is %v", c.name, c.err, c.want)
		}
	}
	if err := newLimiter(0, nil).AddTenant("ok", 1); err != nil {
		t.Errorf("valid weight rejected: %v", err)
	}
}

// 队列上限只挡超限租户；空租户可移除，移除后可重新注册。
func TestBackpressureIsolation(t *testing.T) {
	l := newLimiter(2, map[string]float64{"a": 1, "b": 1, "c": 1})
	for i := 0; i < 2; i++ {
		if err := l.Submit(task.Task{Tenant: "a", Cost: 1}); err != nil {
			t.Fatalf("submit a[%d]: %v", i, err)
		}
	}
	if err := l.Submit(task.Task{Tenant: "a", Cost: 1}); !errors.Is(err, admit.ErrQueueFull) {
		t.Fatalf("a over cap: err=%v", err)
	}
	if err := l.Submit(task.Task{Tenant: "b", Cost: 1}); err != nil {
		t.Errorf("b blocked by a's full queue: %v", err)
	}
	if err := l.RemoveTenant("c"); err != nil {
		t.Errorf("remove idle tenant: %v", err)
	}
	if err := l.AddTenant("c", 2); err != nil {
		t.Errorf("re-add removed tenant: %v", err)
	}
	if l.QueueLen("a") != 2 || l.QueueLen("ghost") != -1 {
		t.Errorf("QueueLen wrong: a=%d ghost=%d", l.QueueLen("a"), l.QueueLen("ghost"))
	}
}

// 8 生产 8 消费并发 10 万任务：总取出数 == 总提交数，序号无重复。
func TestConcurrentExactlyOnce(t *testing.T) {
	const total = 100000
	l := newLimiter(0, map[string]float64{})
	for i := 0; i < 16; i++ {
		l.AddTenant(fmt.Sprintf("t%02d", i), float64(i%3+1))
	}
	var seq atomic.Uint64
	var wg sync.WaitGroup
	for p := 0; p < 8; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < total/8; i++ {
				n := seq.Add(1)
				if err := l.Submit(task.Task{
					Tenant: fmt.Sprintf("t%02d", n%16),
					Cost:   1,
					Seq:    n,
				}); err != nil {
					t.Errorf("submit: %v", err)
					return
				}
			}
		}()
	}
	seen := make([]int32, total+1)
	var mu sync.Mutex
	var drained atomic.Int64
	for c := 0; c < 8; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for drained.Load() < total {
				tk, ok := l.Next()
				if !ok {
					continue
				}
				mu.Lock()
				seen[tk.Seq]++
				mu.Unlock()
				drained.Add(1)
			}
		}()
	}
	wg.Wait()
	for n := 1; n <= total; n++ {
		if seen[n] != 1 {
			t.Fatalf("seq %d seen %d times", n, seen[n])
		}
	}
}

// 同一确定性任务流跑 20 次，调度序列逐元素相同。
func TestDeterminism(t *testing.T) {
	var base []task.Task
	for r := 0; r < 20; r++ {
		l := newLimiter(0, map[string]float64{"u": 1, "v": 2, "w": 3})
		for i := 0; i < 300; i++ {
			l.Submit(task.Task{
				Tenant: []string{"u", "v", "w"}[(i*7)%3],
				Cost:   float64(i%5 + 1),
				Seq:    uint64(i),
			})
		}
		var got []task.Task
		for {
			tk, ok := l.Next()
			if !ok {
				break
			}
			got = append(got, tk)
		}
		if r == 0 {
			base = got
			continue
		}
		if len(got) != len(base) {
			t.Fatalf("run %d: len %d != %d", r, len(got), len(base))
		}
		for i := range base {
			if got[i] != base[i] {
				t.Fatalf("run %d: pick %d = %+v, want %+v", r, i, got[i], base[i])
			}
		}
	}
}
