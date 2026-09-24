package sched

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"ontology/task"
	"ontology/tenant"
)

func build(t *testing.T, weights map[string]float64, submits map[string]int, costs map[string]float64) (*Scheduler, int) {
	t.Helper()
	s := New()
	total := 0
	for id, w := range weights {
		if err := s.AddTenant(id, w); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < submits[id]; i++ {
			if err := s.Submit(task.Task{Tenant: id, Cost: costs[id], Seq: int64(i)}); err != nil {
				t.Fatal(err)
			}
			total++
		}
	}
	return s, total
}

func TestFairShareRatio(t *testing.T) {
	cases := []struct {
		name      string
		wa, wb    float64
		wantRatio float64
	}{
		{"1:3", 1, 3, 3},
		{"1:1", 1, 1, 1},
		{"2:5", 2, 5, 2.5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			na := 10000
			nb := int(float64(na) * tc.wantRatio)
			s, total := build(t, map[string]float64{"a": tc.wa, "b": tc.wb},
				map[string]int{"a": na, "b": nb}, map[string]float64{"a": 1, "b": 1})
			counts := map[string]int{}
			for i := 0; i < total; i++ {
				tk, err := s.Next()
				if err != nil {
					t.Fatal(err)
				}
				counts[tk.Tenant]++
			}
			got := float64(counts["b"]) / float64(counts["a"])
			if dev := math.Abs(got-tc.wantRatio) / tc.wantRatio; dev >= 0.05 {
				t.Errorf("ratio %.4f deviates %.2f%% from %.1f", got, dev*100, tc.wantRatio)
			}
		})
	}
}

func TestShareDeviation(t *testing.T) {
	weights := map[string]float64{"a": 1, "b": 3, "c": 6}
	costs := map[string]float64{"a": 1, "b": 2, "c": 3}
	s, _ := build(t, weights, map[string]int{"a": 100000, "b": 100000, "c": 100000}, costs)
	served := map[string]float64{}
	total := 0.0
	for i := 0; i < 100000; i++ {
		tk, err := s.Next()
		if err != nil {
			t.Fatal(err)
		}
		served[tk.Tenant] += tk.Cost
		total += tk.Cost
	}
	wsum := 0.0
	for _, w := range weights {
		wsum += w
	}
	for id, w := range weights {
		want := w / wsum
		got := served[id] / total
		if dev := math.Abs(got-want) / want; dev >= 0.05 {
			t.Errorf("%s share %.4f deviates %.2f%% from %.4f", id, got, dev*100, want)
		}
	}
}

func TestRejoin(t *testing.T) {
	consecutive := func(boost bool) int {
		x, y := tenant.New("X", 1), tenant.New("Y", 1)
		for i := 0; i < 1000; i++ {
			y.Advance(1)
		}
		if boost {
			x.Boost(y.VT())
		}
		n := 0
		for x.VT() <= y.VT() {
			x.Advance(1)
			n++
		}
		return n
	}
	cases := []struct {
		name           string
		boost          bool
		minRun, maxRun int
	}{
		{"boosted", true, 1, 2},
		{"unboosted", false, 1000, 1002},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if n := consecutive(tc.boost); n < tc.minRun || n > tc.maxRun {
				t.Errorf("X consecutive run %d outside [%d,%d]", n, tc.minRun, tc.maxRun)
			}
		})
	}
	s, _ := build(t, map[string]float64{"X": 1, "Y": 1}, map[string]int{"Y": 1000}, map[string]float64{"Y": 1})
	for i := 0; i < 1000; i++ {
		s.Next()
	}
	s.Submit(task.Task{Tenant: "X", Cost: 1})
	for i := 0; i < 1000; i++ {
		s.Submit(task.Task{Tenant: "Y", Cost: 1, Seq: int64(1000 + i)})
	}
	xAt, yAfter := -1, 0
	for i := 0; i < 1001; i++ {
		tk, _ := s.Next()
		if tk.Tenant == "X" && xAt < 0 {
			xAt = i
		}
		if xAt >= 0 && tk.Tenant == "Y" {
			yAfter++
		}
	}
	if xAt < 0 || xAt > 2 || yAfter != 1000 {
		t.Errorf("X served at %d, Y after %d; want xAt<=2 and Y not starved", xAt, yAfter)
	}
}

func TestTieBreak(t *testing.T) {
	cases := []struct {
		ids  []string
		want []string
	}{
		{[]string{"gamma", "alpha", "beta"}, []string{"alpha", "beta", "gamma"}},
		{[]string{"b", "a"}, []string{"a", "b"}},
		{[]string{"t2", "t10", "t1"}, []string{"t1", "t10", "t2"}},
	}
	for _, tc := range cases {
		s, _ := build(t, map[string]float64{}, map[string]int{}, nil)
		for _, id := range tc.ids {
			s.AddTenant(id, 1)
			s.Submit(task.Task{Tenant: id, Cost: 1})
		}
		for i, want := range tc.want {
			tk, err := s.Next()
			if err != nil || tk.Tenant != want {
				t.Errorf("%v: position %d got %q want %q", tc.ids, i, tk.Tenant, want)
			}
		}
	}
}

func TestComparisons(t *testing.T) {
	cases := []struct{ n, idle int }{{10, 3}, {100, 50}, {1000, 500}}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.n), func(t *testing.T) {
			s := New()
			for i := 0; i < tc.n+tc.idle; i++ {
				s.AddTenant(fmt.Sprintf("t%05d", i), 1)
			}
			for i := 0; i < tc.n; i++ {
				s.Submit(task.Task{Tenant: fmt.Sprintf("t%05d", i), Cost: 1})
				s.Submit(task.Task{Tenant: fmt.Sprintf("t%05d", i), Cost: 1})
			}
			if s.Active() != tc.n {
				t.Fatalf("Active=%d want %d (idle tenants excluded)", s.Active(), tc.n)
			}
			s.Next()
			bound := 4 * int(math.Ceil(math.Log2(float64(tc.n))))
			if got := s.LastComparisons(); got > bound {
				t.Errorf("comparisons %d > bound %d", got, bound)
			}
			for i := 0; i < 2*tc.n-1; i++ {
				s.Next()
			}
			if s.Active() != 0 {
				t.Errorf("Active=%d after drain, want 0", s.Active())
			}
		})
	}
}

func TestErrors(t *testing.T) {
	cases := []struct {
		name string
		op   func(*Scheduler) error
		want error
	}{
		{"zero weight", func(s *Scheduler) error { return s.AddTenant("a", 0) }, ErrInvalidWeight},
		{"negative weight", func(s *Scheduler) error { return s.AddTenant("a", -2) }, ErrInvalidWeight},
		{"nan weight", func(s *Scheduler) error { return s.AddTenant("a", math.NaN()) }, ErrInvalidWeight},
		{"inf weight", func(s *Scheduler) error { return s.AddTenant("a", math.Inf(1)) }, ErrInvalidWeight},
		{"empty next", func(s *Scheduler) error { _, err := s.Next(); return err }, ErrEmpty},
		{"unknown submit", func(s *Scheduler) error { return s.Submit(task.Task{Tenant: "ghost"}) }, ErrUnknownTenant},
		{"unknown remove", func(s *Scheduler) error { return s.RemoveTenant("ghost") }, ErrUnknownTenant},
		{"busy remove", func(s *Scheduler) error {
			s.AddTenant("a", 1)
			s.Submit(task.Task{Tenant: "a", Cost: 1})
			return s.RemoveTenant("a")
		}, ErrTenantBusy},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.op(New()); !errors.Is(err, tc.want) {
				t.Errorf("got %v, want errors.Is %v", err, tc.want)
			}
		})
	}
	s := New()
	s.AddTenant("a", 1)
	s.Submit(task.Task{Tenant: "a", Cost: 1})
	s.Next()
	if err := s.RemoveTenant("a"); err != nil {
		t.Errorf("remove after drain: %v", err)
	}
}

func TestZeroCost(t *testing.T) {
	cases := []struct{ zcost float64 }{{0}, {0.5}}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.zcost), func(t *testing.T) {
			s, _ := build(t, map[string]float64{"Z": 1, "W": 1},
				map[string]int{"Z": 100, "W": 100}, map[string]float64{"Z": tc.zcost, "W": 1})
			run, maxRun := 0, 0
			for i := 0; i < 200; i++ {
				tk, _ := s.Next()
				if tk.Tenant == "Z" {
					run++
					maxRun = max(maxRun, run)
				} else {
					run = 0
				}
			}
			if maxRun > 2 {
				t.Errorf("zero-cost tenant monopolized: max run %d", maxRun)
			}
		})
	}
}

func TestDeterminism(t *testing.T) {
	seq := func() string {
		s, _ := build(t, map[string]float64{"p": 2, "q": 1}, map[string]int{}, nil)
		out := ""
		for i := 0; i < 2000; i++ {
			id := "p"
			if i%3 == 0 {
				id = "q"
			}
			s.Submit(task.Task{Tenant: id, Cost: float64(i % 7), Seq: int64(i)})
			tk, _ := s.Next()
			out += tk.Tenant
		}
		return out
	}
	base := seq()
	for i := 0; i < 20; i++ {
		if got := seq(); got != base {
			t.Fatalf("run %d differs", i)
		}
	}
}

func TestConcurrency(t *testing.T) {
	s := New()
	s.AddTenant("c1", 1)
	s.AddTenant("c2", 1)
	const total = 100000
	var wg sync.WaitGroup
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "c1"
			if i%2 == 0 {
				id = "c2"
			}
			s.Submit(task.Task{Tenant: id, Cost: 1, Seq: int64(i)})
		}(i)
	}
	wg.Wait()
	seen := make([]bool, total)
	var mu sync.Mutex
	count := 0
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				tk, err := s.Next()
				if err != nil {
					return
				}
				mu.Lock()
				if !seen[tk.Seq] {
					seen[tk.Seq] = true
					count++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if count != total {
		t.Errorf("dequeued %d, want %d (no loss, no dup)", count, total)
	}
}

func TestInjectedClock(t *testing.T) {
	now := time.Unix(0, 0)
	s := New(WithClock(func() time.Time { return now }))
	s.AddTenant("a", 1)
	s.Submit(task.Task{Tenant: "a", Cost: 1})
	now = time.Unix(42, 0)
	s.Next()
	if !s.LastServedAt().Equal(time.Unix(42, 0)) {
		t.Errorf("LastServedAt=%v, want injected clock value", s.LastServedAt())
	}
}
