package ddl

import (
	"fmt"
	"sync"
	"testing"
)

// 第四节：第一次 Backfill 检查约 m 个键；立即第二次检查数不随 m 增长（为 0）。
func TestBackfillChecksOnlyNewKeys(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			s := New(1 << 30)
			for i := 0; i < m; i++ {
				if err := s.Put(fmt.Sprintf("k%d", i), 1); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.BeginMigration(); err != nil {
				t.Fatal(err)
			}
			if err := s.Backfill(); err != nil {
				t.Fatal(err)
			}
			if s.lastChecked != m {
				t.Fatalf("first Backfill checked %d keys, want %d", s.lastChecked, m)
			}
			if err := s.Backfill(); err != nil {
				t.Fatal(err)
			}
			if s.lastChecked != 0 {
				t.Fatalf("second Backfill checked %d keys, want 0 (must not grow with m)", s.lastChecked)
			}
		})
	}
}

// 阶段转移合法性：只能向前、不能跳（表驱动）。
func TestPhaseTransitions(t *testing.T) {
	cases := []struct {
		name     string
		ops      []func(*Store) error
		wantErrs []bool
	}{
		{"normal flow", []func(*Store) error{(*Store).BeginMigration, (*Store).Backfill, (*Store).Switch}, []bool{false, false, false}},
		{"skip to switch", []func(*Store) error{(*Store).Switch}, []bool{true}},
		{"double begin", []func(*Store) error{(*Store).BeginMigration, (*Store).BeginMigration}, []bool{false, true}},
		{"backfill in normal", []func(*Store) error{(*Store).Backfill}, []bool{true}},
		{"backfill after switch", []func(*Store) error{(*Store).BeginMigration, (*Store).Switch, (*Store).Backfill}, []bool{false, false, true}},
		{"begin after switch", []func(*Store) error{(*Store).BeginMigration, (*Store).Switch, (*Store).BeginMigration}, []bool{false, false, true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New(10)
			for i, op := range tc.ops {
				if err := op(s); (err != nil) != tc.wantErrs[i] {
					t.Fatalf("op %d err=%v, wantErr=%v", i, err, tc.wantErrs[i])
				}
			}
		})
	}
}

// 不变量3：Backfill 任意次结果不变（表驱动多档次数）。
func TestBackfillIdempotent(t *testing.T) {
	for _, times := range []int{1, 2, 3, 7} {
		t.Run(fmt.Sprintf("times=%d", times), func(t *testing.T) {
			s := New(100)
			_ = s.Put("a", 3)
			_ = s.Put("b", 5)
			_ = s.BeginMigration()
			for i := 0; i < times; i++ {
				if err := s.Backfill(); err != nil {
					t.Fatal(err)
				}
			}
			_ = s.Switch()
			if s.Get("a") != 6 || s.Get("b") != 10 {
				t.Fatalf("after %d backfills: a=%d b=%d, want 6/10", times, s.Get("a"), s.Get("b"))
			}
		})
	}
}

// 并发：N 个 goroutine 读同一键结果一致；并发写不同键后各键正确。不用 sleep。
func TestConcurrent(t *testing.T) {
	s := New(1 << 30)
	if err := s.Put("hot", 42); err != nil {
		t.Fatal(err)
	}
	const n = 32
	start := make(chan struct{})
	var wg sync.WaitGroup
	got := make([][]int, n)
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			<-start
			got[i] = make([]int, 50)
			for j := range got[i] {
				got[i][j] = s.Get("hot")
			}
		}(i)
		go func(i int) {
			defer wg.Done()
			<-start
			_ = s.Put(fmt.Sprintf("w%d", i), i+1)
		}(i)
	}
	close(start)
	wg.Wait()
	for i := 0; i < n; i++ {
		for _, v := range got[i] {
			if v != 42 {
				t.Fatalf("concurrent read got %d, want 42", v)
			}
		}
		if s.Get(fmt.Sprintf("w%d", i)) != i+1 {
			t.Fatalf("Get(w%d)=%d, want %d", i, s.Get(fmt.Sprintf("w%d", i)), i+1)
		}
	}
}
