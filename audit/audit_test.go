package audit_test

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"ontology/alloc"
	"ontology/audit"
	"ontology/clock"
	"ontology/durable"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestAnalyze(t *testing.T) {
	tests := []struct {
		name    string
		streams [][]uint64
		want    audit.Report
	}{
		{"空输入", nil, audit.Report{Monotonic: true}},
		{"唯一且连续", [][]uint64{{1, 2, 3}, {4, 5}}, audit.Report{Total: 5, Monotonic: true}},
		{"跨实例重复", [][]uint64{{1, 2}, {2, 3}}, audit.Report{Total: 4, Duplicates: 1, Monotonic: true}},
		{"实例内非单调", [][]uint64{{3, 1, 2}}, audit.Report{Total: 3, Monotonic: false}},
		{"单个空洞", [][]uint64{{1, 2, 5}}, audit.Report{Total: 3, Monotonic: true, Gaps: 1, GapLength: 2}},
		{"多个空洞", [][]uint64{{1, 4}, {8}}, audit.Report{Total: 3, Monotonic: true, Gaps: 2, GapLength: 5}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := audit.Analyze(tc.streams...); got != tc.want {
				t.Fatalf("Analyze()=%+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestUniqueAcrossInstances(t *testing.T) {
	ctr, err := durable.Open(filepath.Join(t.TempDir(), "counter"), 0)
	if err != nil {
		t.Fatal(err)
	}
	const instances, per = 4, 25000
	streams := make([][]uint64, instances)
	var wg sync.WaitGroup
	for i := 0; i < instances; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a, err := alloc.New(ctr, clock.Real{}, time.Minute, 1, 1024)
			if err != nil {
				t.Error(err)
				return
			}
			for j := 0; j < per; j++ {
				v, err := a.Alloc()
				if err != nil {
					t.Error(err)
					return
				}
				streams[i] = append(streams[i], v)
			}
		}(i)
	}
	wg.Wait()
	r := audit.Analyze(streams...)
	if r.Total != instances*per || r.Duplicates != 0 {
		t.Fatalf("总数=%d 重复=%d, want %d 个互不重复", r.Total, r.Duplicates, instances*per)
	}
	if !r.Monotonic {
		t.Fatal("实例内分发应严格递增")
	}
}

func TestGapLengthEquation(t *testing.T) {
	dir := t.TempDir()
	clk := clock.NewFake(t0)
	newAlloc := func() *alloc.Allocator {
		ctr, err := durable.Open(filepath.Join(dir, "counter"), 0)
		if err != nil {
			t.Fatal(err)
		}
		a, err := alloc.New(ctr, clk, 10*time.Second, 10, 10)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	a := newAlloc()
	var sa, sb []uint64
	for i := 0; i < 3; i++ { // 租 [0,10)，发 0,1,2
		v, _ := a.Alloc()
		sa = append(sa, v)
	}
	clk.Advance(11 * time.Second) // [0,10) 作废，丢弃 3..9 共 7 个
	v, _ := a.Alloc()             // 租 [10,20)，发 10
	sa = append(sa, v)
	// 模拟崩溃：丢弃 a（其租约 [10,20) 剩余 11..19 共 9 个）
	b := newAlloc()
	for i := 0; i < 4; i++ { // 租 [20,30)，发 20..23
		v, err := b.Alloc()
		if err != nil {
			t.Fatal(err)
		}
		sb = append(sb, v)
	}
	r := audit.Analyze(sa, sb)
	const expired, crashed = 7, 9
	if r.GapLength != expired+crashed || r.Gaps != 2 {
		t.Fatalf("空洞=%d 段/%d 个, want 2 段/%d 个（作废剩余+崩溃丢弃）",
			r.Gaps, r.GapLength, expired+crashed)
	}
	if r.Duplicates != 0 || !r.Monotonic {
		t.Fatalf("重复=%d 单调=%v, want 0/true", r.Duplicates, r.Monotonic)
	}
}
