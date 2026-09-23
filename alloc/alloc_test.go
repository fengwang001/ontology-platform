package alloc

import (
	"errors"
	"math"
	"path/filepath"
	"testing"
	"time"

	"ontology/clock"
	"ontology/durable"
	"ontology/lease"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func openCtr(t *testing.T, dir string, start uint64) *durable.Counter {
	t.Helper()
	c, err := durable.Open(filepath.Join(dir, "counter"), start)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mustAlloc(t *testing.T, c *durable.Counter, clk clock.Clock, ttl time.Duration, lo, hi int64) *Allocator {
	t.Helper()
	a, err := New(c, clk, ttl, lo, hi)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestCrashMidDistribution(t *testing.T) {
	dir := t.TempDir()
	a := mustAlloc(t, openCtr(t, dir, 100), clock.NewFake(t0), time.Minute, 100, 100)
	for i := 0; i < 50; i++ { // 租到 [100,200)，分发到 150 时"崩溃"
		if _, err := a.Alloc(); err != nil {
			t.Fatal(err)
		}
	}
	b := mustAlloc(t, openCtr(t, dir, 100), clock.NewFake(t0), time.Minute, 100, 100)
	first, err := b.Alloc()
	if err != nil {
		t.Fatal(err)
	}
	if first < 200 {
		t.Fatalf("重启后首号=%d, want >= 200（租段末尾），否则重号", first)
	}
}

func TestExpiryDiscardsOldSegment(t *testing.T) {
	clk := clock.NewFake(t0)
	a := mustAlloc(t, openCtr(t, t.TempDir(), 0), clk, 10*time.Second, 10, 10)
	got := make([]uint64, 0, 8)
	for i := 0; i < 3; i++ {
		v, _ := a.Alloc()
		got = append(got, v)
	}
	clk.Advance(11 * time.Second) // 跨过到期点，旧段 [0,10) 作废
	for i := 0; i < 5; i++ {
		v, err := a.Alloc()
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	for _, v := range got[3:] {
		if v < 10 {
			t.Fatalf("到期后仍分发旧段的号 %d", v)
		}
	}
	for _, old := range []uint64{3, 4, 5, 6, 7, 8, 9} { // 旧段剩余永不出现
		for _, v := range got {
			if v == old {
				t.Fatalf("旧段剩余号 %d 被重复分发", old)
			}
		}
	}
	if got[3] != 10 {
		t.Fatalf("到期后首号=%d, want 10（新段起点）", got[3])
	}
}

func TestClockBackwards(t *testing.T) {
	clk := clock.NewFake(t0)
	a := mustAlloc(t, openCtr(t, t.TempDir(), 0), clk, 10*time.Second, 4, 4)
	if _, err := a.Alloc(); err != nil {
		t.Fatal(err)
	}
	clk.Advance(-time.Hour) // 回拨：不得让租约"复活"，必须拒绝
	if _, err := a.Alloc(); !errors.Is(err, ErrClockBackwards) {
		t.Fatalf("err=%v, want ErrClockBackwards", err)
	}
}

func TestPersistWritesAndO1(t *testing.T) {
	a := mustAlloc(t, openCtr(t, t.TempDir(), 0), clock.NewFake(t0), time.Hour, 1, 1000)
	const total = 100000
	for i := 0; i < total; i++ {
		if _, err := a.Alloc(); err != nil {
			t.Fatal(err)
		}
	}
	if w := a.PersistWrites(); w > 120 {
		t.Fatalf("持久写次数=%d, want <= 120", w)
	}
	if a.Ops()+a.PersistWrites() != total {
		t.Fatalf("快速路径操作数=%d 非常数（每次分发应恰为 1）", a.Ops())
	}
}

func TestAdaptiveSegmentLength(t *testing.T) {
	clk := clock.NewFake(t0)
	a := mustAlloc(t, openCtr(t, t.TempDir(), 0), clk, 10*time.Second, 1, 256)
	avg := func(h []lease.Lease) float64 {
		var sum uint64
		for _, l := range h {
			sum += l.Length
		}
		return float64(sum) / float64(len(h))
	}
	for i := 0; i < 2000; i++ { // 高频：段被耗尽，段长倍增至封顶
		if _, err := a.Alloc(); err != nil {
			t.Fatal(err)
		}
	}
	high := avg(a.History())
	mark := len(a.History())
	for i := 0; i < 100; i++ { // 低频：每次分配前跨过租期，段长回落
		clk.Advance(11 * time.Second)
		if _, err := a.Alloc(); err != nil {
			t.Fatal(err)
		}
	}
	low := avg(a.History()[mark:])
	if low == 0 || high < 4*low {
		t.Fatalf("高频均段长=%.1f 低频=%.2f, want 高频显著大于低频", high, low)
	}
}

func TestBoundary(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"非法段长配置", func(t *testing.T) {
			c := openCtr(t, t.TempDir(), 0)
			for _, mm := range [][2]int64{{0, 1}, {-1, 5}, {10, 5}} {
				if _, err := New(c, clock.NewFake(t0), time.Second, mm[0], mm[1]); !errors.Is(err, lease.ErrInvalidLength) {
					t.Fatalf("min=%d max=%d: err=%v, want ErrInvalidLength", mm[0], mm[1], err)
				}
			}
		}},
		{"租期为零每次重租", func(t *testing.T) {
			a := mustAlloc(t, openCtr(t, t.TempDir(), 0), clock.NewFake(t0), 0, 1, 8)
			for i := 0; i < 5; i++ {
				if _, err := a.Alloc(); err != nil {
					t.Fatal(err)
				}
			}
			if a.PersistWrites() != 5 {
				t.Fatalf("租期 0 时持久写=%d, want 5（每次分配都重租）", a.PersistWrites())
			}
		}},
		{"段长为一每次都写", func(t *testing.T) {
			a := mustAlloc(t, openCtr(t, t.TempDir(), 0), clock.NewFake(t0), time.Hour, 1, 1)
			for i := 0; i < 7; i++ {
				if _, err := a.Alloc(); err != nil {
					t.Fatal(err)
				}
			}
			if a.PersistWrites() != 7 {
				t.Fatalf("段长 1 时持久写=%d, want 7", a.PersistWrites())
			}
		}},
		{"计数器到顶拒绝回绕", func(t *testing.T) {
			a := mustAlloc(t, openCtr(t, t.TempDir(), math.MaxUint64-1), clock.NewFake(t0), time.Hour, 8, 8)
			if _, err := a.Alloc(); !errors.Is(err, durable.ErrOverflow) {
				t.Fatalf("err=%v, want ErrOverflow", err)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}
