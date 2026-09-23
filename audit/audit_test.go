package audit

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"ontology/alloc"
	"ontology/clock"
	"ontology/durable"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestAnalyze(t *testing.T) {
	cases := []struct {
		name    string
		streams [][]uint64
		want    Report
	}{
		{"unique monotonic no gap", [][]uint64{{0, 1, 2}, {3, 4, 5}},
			Report{Total: 6, Unique: true, Monotonic: true}},
		{"duplicate across streams", [][]uint64{{0, 1}, {1, 2}},
			Report{Total: 4, Unique: false, Monotonic: true, Duplicates: 1}},
		{"not monotonic", [][]uint64{{0, 5, 3}},
			Report{Total: 3, Unique: true, Monotonic: false, GapRanges: 2, GapTotal: 3}},
		{"gap ranges and total", [][]uint64{{0, 1, 5, 6, 10}},
			Report{Total: 5, Unique: true, Monotonic: true, GapRanges: 2, GapTotal: 6}},
		{"empty", nil, Report{Unique: true, Monotonic: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Analyze(tc.streams...); got != tc.want {
				t.Fatalf("Analyze = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// 空洞等式：空洞总长度 == 作废号段剩余长度 + 崩溃丢弃长度（精确断言）。
func TestGapEquation(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "counter")
	ctr, err := durable.Open(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	cfg := alloc.Config{TTL: time.Minute, MinSeg: 100, MaxSeg: 100}
	clk := clock.NewFake(t0)
	a := alloc.New(clk, ctr, cfg)
	var streamA []uint64
	for i := 0; i < 50; i++ { // 租 [0,100)，发 50 个后崩溃，丢弃 50
		id, err := a.Next()
		if err != nil {
			t.Fatal(err)
		}
		streamA = append(streamA, id)
	}
	ctr2, err := durable.Open(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	b := alloc.New(clk, ctr2, cfg)
	var streamB []uint64
	for i := 0; i < 10; i++ { // 租 [100,200)，发 10 个
		id, err := b.Next()
		if err != nil {
			t.Fatal(err)
		}
		streamB = append(streamB, id)
	}
	clk.Advance(2 * time.Minute) // 到期，作废 110..199 共 90 个
	for i := 0; i < 100; i++ {   // 租 [200,300)，发完
		id, err := b.Next()
		if err != nil {
			t.Fatal(err)
		}
		streamB = append(streamB, id)
	}
	const crashDiscarded, leaseVoided = 50, 90
	r := Analyze(streamA, streamB)
	if !r.Unique || !r.Monotonic {
		t.Fatalf("unique=%v monotonic=%v", r.Unique, r.Monotonic)
	}
	if r.GapRanges != 2 {
		t.Fatalf("gap ranges = %d, want 2", r.GapRanges)
	}
	if r.GapTotal != crashDiscarded+leaseVoided {
		t.Fatalf("gap total = %d, want %d", r.GapTotal, crashDiscarded+leaseVoided)
	}
}

// 4 实例共享同一持久计数器，并发各分发 2.5 万，10 万号互不重复且各自单调。
func TestConcurrentUniqueness(t *testing.T) {
	ctr, err := durable.Open(filepath.Join(t.TempDir(), "counter"), 0)
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(t0)
	streams := make([][]uint64, 4)
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		a := alloc.New(clk, ctr, alloc.Config{TTL: time.Hour})
		wg.Add(1)
		go func(g int, a *alloc.Allocator) {
			defer wg.Done()
			for i := 0; i < 25000; i++ {
				id, err := a.Next()
				if err != nil {
					t.Error(err)
					return
				}
				streams[g] = append(streams[g], id)
			}
		}(g, a)
	}
	wg.Wait()
	r := Analyze(streams...)
	if r.Total != 100000 || !r.Unique || !r.Monotonic {
		t.Fatalf("total=%d unique=%v monotonic=%v", r.Total, r.Unique, r.Monotonic)
	}
}
