package schema

import (
	"errors"
	"math"
	"sync"
	"testing"
)

func TestRegisterVersionChain(t *testing.T) {
	r := NewRegistry()
	cases := []struct {
		name   string
		bounds []int64
		ver    int
		err    error
	}{
		{"lat", []int64{10, 20, 50, 100}, 1, nil},
		{"lat", []int64{20, 100}, 2, nil},                // 真子集
		{"lat", []int64{20, 50, 70}, 0, ErrIncompatible}, // 非子集非超集
		{"lat", []int64{20, 100}, 0, ErrUnchanged},       // 与最新版相等
		{"lat", []int64{20, 50, 100, 200}, 3, nil},       // v2 真超集
		{"lat", []int64{20, 50, 100, 200}, 0, ErrUnchanged},
		{"", []int64{1}, 0, ErrInvalidArgument},
		{"bad", []int64{}, 0, ErrInvalidArgument},
		{"bad", []int64{5, 4}, 0, ErrInvalidArgument},
		{"bad", []int64{0}, 0, ErrInvalidArgument},
		{"bad", []int64{1_000_000_000_001}, 0, ErrInvalidArgument},
	}
	for _, c := range cases {
		v, err := r.Register(c.name, c.bounds)
		t.Logf("Register(%q,%v) -> v=%d err=%v（判定：%v）", c.name, c.bounds, v, err, c.err)
		if !errors.Is(err, c.err) || (err == nil && v != c.ver) {
			t.Fatalf("Register %q %v = (%d,%v), want (%d,%v)", c.name, c.bounds, v, err, c.ver, c.err)
		}
	}
	got, err := r.Bounds("lat", 1)
	if err != nil || len(got) != 4 || got[0] != 10 {
		t.Fatalf("Bounds v1 = %v,%v", got, err)
	}
	got[0] = 999
	again, _ := r.Bounds("lat", 1)
	if again[0] != 10 {
		t.Fatal("Bounds 必须返回副本")
	}
	if _, err := r.Bounds("nope", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在名字 err=%v", err)
	}
	if _, err := r.Bounds("lat", 9); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在版本 err=%v", err)
	}
}

func TestRejectOrderUnchangedBeforeIncompatible(t *testing.T) {
	r := NewRegistry()
	r.Register("x", []int64{1, 2})
	_, err := r.Register("x", []int64{1, 2})
	if !errors.Is(err, ErrUnchanged) {
		t.Fatalf("相等边界应为无变化错误, got %v", err)
	}
}

func TestHistValid(t *testing.T) {
	good := Hist{Name: "h", Bounds: []int64{10, 20}, Counts: []uint64{1, 2, 0}, Sum: 30}
	if !good.Valid() {
		t.Fatal("合法 Hist 被判非法")
	}
	bad := []Hist{
		{Bounds: []int64{10}, Counts: []uint64{1}, Sum: 0}, // 桶数不符
		{Bounds: []int64{10}, Counts: []uint64{0, math.MaxInt64 + 1}, Sum: 0},
		{Bounds: []int64{10, 5}, Counts: []uint64{0, 0, 0}, Sum: 0}, // 非递增
		{Bounds: []int64{10}, Counts: []uint64{0, 0}, Sum: -1},
	}
	for i, h := range bad {
		t.Logf("非法 Hist#%d: %+v -> Valid=%v", i, h, h.Valid())
		if h.Valid() {
			t.Fatalf("非法 Hist#%d 被判合法", i)
		}
	}
}

// TestCollectorBuckets 与朴素规则逐值对照：桶 i 收纳 Bounds[i-1] < v <= Bounds[i]，桶 0 含 0。
func TestCollectorBuckets(t *testing.T) {
	r := NewRegistry()
	r.Register("lat", []int64{10, 20, 50, 100})
	c, err := NewCollector(r, "lat", 1)
	if err != nil {
		t.Fatal(err)
	}
	vals := []int64{0, 10, 11, 20, 21, 50, 51, 100, 101, 1_000_000_000_000}
	want := make([]uint64, 5)
	var sum int64
	for _, v := range vals {
		if err := c.Observe(v); err != nil {
			t.Fatalf("Observe(%d): %v", v, err)
		}
		switch {
		case v <= 10:
			want[0]++
		case v <= 20:
			want[1]++
		case v <= 50:
			want[2]++
		case v <= 100:
			want[3]++
		default:
			want[4]++
		}
		sum += v
	}
	h := c.Snapshot()
	t.Logf("输入=%v 朴素期望 counts=%v sum=%d；实际=%+v", vals, want, sum, h)
	for i := range want {
		if h.Counts[i] != want[i] {
			t.Fatalf("桶 %d: got %d want %d（边界恰等归属/桶0含0）", i, h.Counts[i], want[i])
		}
	}
	if h.Sum != sum {
		t.Fatalf("Sum=%d want %d", h.Sum, sum)
	}
	if err := c.Observe(-1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("负数应参数非法: %v", err)
	}
	if err := c.Observe(1_000_000_000_001); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("超范围应参数非法: %v", err)
	}
	if _, err := NewCollector(r, "nope", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("无此模式: %v", err)
	}
	h.Counts[0] = 99
	again := c.Snapshot()
	if again.Counts[0] == 99 {
		t.Fatal("Snapshot 必须是独立副本")
	}
}

func TestObserveOverflowLeavesState(t *testing.T) {
	r := NewRegistry()
	r.Register("s", []int64{10})
	c, _ := NewCollector(r, "s", 1)
	c.sum = math.MaxInt64 - 4
	before := c.Snapshot()
	err := c.Observe(5)
	if !errors.Is(err, ErrOverflow) {
		t.Fatalf("期望溢出错误, got %v", err)
	}
	after := c.Snapshot()
	after2 := c.Snapshot()
	if after.Sum != before.Sum || after.Counts[0] != before.Counts[0] {
		t.Fatal("溢出后状态被修改")
	}
	_ = after2
}

func TestConcurrentObserveSnapshot(t *testing.T) {
	r := NewRegistry()
	r.Register("c", []int64{100})
	c, _ := NewCollector(r, "c", 1)
	const goroutines, per = 16, 2000
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < per; i++ {
				if err := c.Observe(1); err != nil {
					t.Errorf("Observe: %v", err)
					return
				}
				s := c.Snapshot()
				var n uint64
				for _, x := range s.Counts {
					n += x
				}
				if int64(n) != s.Sum {
					t.Errorf("非一致快照: N=%d Sum=%d（必须是某个 Observe 前缀）", n, s.Sum)
				}
				if n > goroutines*per {
					t.Errorf("快照计数 %d 超过已完成观测上限", n)
				}
			}
		}()
	}
	wg.Wait()
	s := c.Snapshot()
	var n uint64
	for _, x := range s.Counts {
		n += x
	}
	t.Logf("并发结束 counts=%v N=%d（期望 %d）Sum=%d", s.Counts, n, goroutines*per, s.Sum)
	if n != goroutines*per {
		t.Fatalf("计数之和=%d, want %d", n, goroutines*per)
	}
}
