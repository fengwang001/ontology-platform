package alloc

import (
	"errors"
	"math"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"ontology/clock"
	"ontology/durable"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func newCounter(t *testing.T, start uint64) *durable.Counter {
	t.Helper()
	return openAt(t, filepath.Join(t.TempDir(), "counter"), start)
}

func openAt(t *testing.T, p string, start uint64) *durable.Counter {
	t.Helper()
	c, err := durable.Open(p, start)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func nextN(t *testing.T, a *Allocator, n int) []uint64 {
	t.Helper()
	ids := make([]uint64, 0, n)
	for i := 0; i < n; i++ {
		id, err := a.Next()
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

// 崩溃于分发中途：重启后首号 >= 已租段末尾。
func TestCrashRecovery(t *testing.T) {
	p := filepath.Join(t.TempDir(), "counter")
	cfg := Config{TTL: time.Hour, MinSeg: 100, MaxSeg: 100}
	ids := nextN(t, New(clock.NewFake(t0), openAt(t, p, 100), cfg), 50) // 发到 149 后"崩溃"
	if ids[49] != 149 {
		t.Fatalf("last = %d, want 149", ids[49])
	}
	first, err := New(clock.NewFake(t0), openAt(t, p, 100), cfg).Next()
	if err != nil || first < 200 {
		t.Fatalf("first id after crash = %d, %v; want >= 200", first, err)
	}
}

// 租约到期：旧段剩余号永不出现。
func TestExpiryVoid(t *testing.T) {
	clk := clock.NewFake(t0)
	a := New(clk, newCounter(t, 0), Config{TTL: time.Minute, MinSeg: 100, MaxSeg: 100})
	nextN(t, a, 10)
	clk.Advance(2 * time.Minute)
	got := map[uint64]bool{}
	for _, id := range nextN(t, a, 100) {
		got[id] = true
	}
	for voided := uint64(10); voided < 100; voided++ {
		if got[voided] {
			t.Fatalf("voided id %d reappeared", voided)
		}
	}
	if !got[100] {
		t.Fatal("expected ids from new segment starting at 100")
	}
}

// 时钟回拨：拒绝分配，过期租约不复活。
func TestClockRollback(t *testing.T) {
	clk := clock.NewFake(t0)
	a := New(clk, newCounter(t, 0), Config{TTL: time.Minute})
	nextN(t, a, 1)
	clk.Advance(-time.Second)
	if _, err := a.Next(); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("err = %v, want ErrClockRollback", err)
	}
	clk.Advance(2 * time.Minute) // 跨过到期点
	id, err := a.Next()
	if err != nil || id != 64 { // 新段起点 = 旧段末尾（默认 MinSeg=64）
		t.Fatalf("id = %d, %v; want 64 (new segment)", id, err)
	}
}

// 持久写次数上界 + 本地分发 O(1)。
func TestPersistWritesBound(t *testing.T) {
	a := New(clock.NewFake(t0), newCounter(t, 0), Config{TTL: time.Hour, MinSeg: 1000, MaxSeg: 1000})
	nextN(t, a, 100000)
	if w := a.PersistWrites(); w > 120 {
		t.Fatalf("persist writes = %d, want <= 120", w)
	}
	if a.ops != 100000 {
		t.Fatalf("ops = %d, want 100000 (O(1) per Next)", a.ops)
	}
}

// 段长自适应：高频阶段平均段长显著大于低频阶段。
func TestAdaptiveSegLen(t *testing.T) {
	clk := clock.NewFake(t0)
	a := New(clk, newCounter(t, 0), Config{TTL: time.Hour, MinSeg: 64, MaxSeg: 4096})
	var hiSum, loSum int64
	for i := 0; i < 30000; i++ {
		nextN(t, a, 1)
		if i%1000 == 0 {
			hiSum += a.SegLen()
		}
	}
	for i := 0; i < 20; i++ {
		clk.Advance(2 * time.Hour)
		nextN(t, a, 1)
		loSum += a.SegLen()
	}
	hiAvg, loAvg := hiSum/30, loSum/20
	if hiAvg < 3000 || loAvg > 300 || hiAvg < 4*loAvg {
		t.Fatalf("hiAvg=%d loAvg=%d, want hi>=3000, lo<=300, hi>=4*lo", hiAvg, loAvg)
	}
}

// 并发租用：8 协程跨实例共享同一计数器，号段不得重叠。
func TestConcurrentLease(t *testing.T) {
	ctr := newCounter(t, 0)
	clk := clock.NewFake(t0)
	ids := make([][]uint64, 8)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		a := New(clk, ctr, Config{TTL: time.Hour})
		wg.Add(1)
		go func(g int, a *Allocator) {
			defer wg.Done()
			for i := 0; i < 5000; i++ {
				id, err := a.Next()
				if err != nil {
					t.Error(err)
					return
				}
				ids[g] = append(ids[g], id)
			}
		}(g, a)
	}
	wg.Wait()
	seen := map[uint64]bool{}
	for _, s := range ids {
		for _, id := range s {
			if seen[id] {
				t.Fatalf("duplicate id %d", id)
			}
			seen[id] = true
		}
	}
	if len(seen) != 40000 {
		t.Fatalf("got %d unique ids, want 40000", len(seen))
	}
}

// 边界语义：段长 1、TTL 0、uint64 上界、写失败。
func TestBoundaries(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"seg len 1 persists every time", Config{TTL: time.Hour, MinSeg: 1, MaxSeg: 1}},
		{"ttl 0 re-leases every time", Config{TTL: 0, MinSeg: 64, MaxSeg: 64}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := New(clock.NewFake(t0), newCounter(t, 0), tc.cfg)
			nextN(t, a, 10)
			if w := a.PersistWrites(); w != 10 {
				t.Fatalf("persist writes = %d, want 10", w)
			}
		})
	}
	t.Run("uint64 overflow rejected", func(t *testing.T) {
		a := New(clock.NewFake(t0), newCounter(t, math.MaxUint64-10), Config{TTL: time.Hour})
		if _, err := a.Next(); !errors.Is(err, durable.ErrOverflow) {
			t.Fatalf("err = %v, want durable.ErrOverflow", err)
		}
	})
	t.Run("write fault fails lease without distributing", func(t *testing.T) {
		ctr := newCounter(t, 0)
		a := New(clock.NewFake(t0), ctr, Config{TTL: time.Hour})
		ctr.InjectFault(errors.New("disk full"))
		if _, err := a.Next(); !errors.Is(err, durable.ErrWrite) {
			t.Fatalf("err = %v, want durable.ErrWrite", err)
		}
		if ctr.Value() != 0 {
			t.Fatalf("counter = %d, want 0 (untouched)", ctr.Value())
		}
		ctr.InjectFault(nil)
		nextN(t, a, 1)
	})
}
