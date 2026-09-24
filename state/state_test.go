package state

import (
	"strconv"
	"testing"
)

// 不变量 1：任意操作序列后与朴素参照一致。
func TestMatchesNaive(t *testing.T) {
	tb := New(10)
	type kv struct {
		val  string
		last int64
	}
	model := map[string]kv{}
	rng := uint64(999)
	next := func(n int64) int64 {
		rng = rng*6364136223846793005 + 1442695040888963407
		return int64(rng>>33) % n
	}
	for step := 0; step < 5000; step++ {
		key := "k" + strconv.Itoa(int(next(8)))
		now := next(80)
		switch next(3) {
		case 0:
			et, v := next(60), "v"+strconv.Itoa(step)
			if err := tb.Put(key, v, et); err != nil {
				t.Fatal(err)
			}
			e := model[key]
			if et > e.last {
				e.last = et
			}
			e.val = v
			model[key] = e
		case 1:
			got, hit, err := tb.Get(key, now)
			if err != nil {
				t.Fatal(err)
			}
			e, exist := model[key]
			want := exist && now-e.last < 10
			if hit != want || (hit && got != e.val) {
				t.Fatalf("Get(%s,%d)=%q,%v want hit=%v val=%q", key, now, got, hit, want, e.val)
			}
			if exist && !want {
				delete(model, key)
			}
		case 2:
			tb.Cleanup(now)
			for k, e := range model {
				if now-e.last >= 10 {
					delete(model, k)
				}
			}
			if len(tb.ents) != len(model) {
				t.Fatalf("after Cleanup(%d): %d entries, want %d", now, len(tb.ents), len(model))
			}
			for k, e := range model {
				if tb.ents[k] != e {
					t.Fatalf("entry %s=%+v, want %+v", k, tb.ents[k], e)
				}
			}
		}
	}
}

// 不变量 2：过期后被匹配即无匹配（含 now-last==ttl 边界）。
func TestExpiredIsNoMatch(t *testing.T) {
	cases := []struct {
		name string
		last int64
		now  int64
		hit  bool
	}{
		{"boundary-expired", 100, 110, false},
		{"one-before-boundary", 100, 109, true},
		{"far-past", 100, 500, false},
		{"fresh", 100, 100, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tb := New(10)
			tb.Put("k", "v", c.last)
			_, hit, err := tb.Get("k", c.now)
			if err != nil || hit != c.hit {
				t.Fatalf("Get=%v,%v want hit=%v", hit, err, c.hit)
			}
			if !c.hit && len(tb.ents) != 0 {
				t.Fatal("expired entry not lazily removed")
			}
		})
	}
	// Cleanup 路径：过期条目从表中消失
	tb := New(10)
	tb.Put("a", "x", 100)
	tb.Put("b", "y", 105)
	if n := tb.Cleanup(110); n != 2 || len(tb.ents) != 0 {
		t.Fatalf("Cleanup removed %d left %d, want 2/0", n, len(tb.ents))
	}
}

// 不变量 3：last 单调，乱序/迟到事件不回退。
func TestLastMonotonic(t *testing.T) {
	tb := New(10)
	tb.Put("k", "A", 200)
	tb.Put("k", "B", 50)  // 迟到
	tb.Put("k", "C", 150) // 乱序但仍旧
	if tb.ents["k"].last != 200 {
		t.Fatalf("last=%d regressed, want 200", tb.ents["k"].last)
	}
	if v, hit, _ := tb.Get("k", 205); !hit || v != "C" {
		t.Fatalf("Get=%q,%v want C,true", v, hit)
	}
}

// 复杂度约束：Cleanup 扫描数 <= 清除数 + 小常数，不随 m 增长。
func TestCleanupScanBounded(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		tb := New(10)
		for i := 0; i < m; i++ {
			tb.Put("k"+strconv.Itoa(i), "v", int64(i))
		}
		removed := tb.Cleanup(12) // 仅 last=0,1,2 共 3 条过期
		if removed != 3 {
			t.Fatalf("m=%d removed=%d, want 3", m, removed)
		}
		if tb.scanned > removed+1 {
			t.Fatalf("m=%d scanned=%d > removed+1=%d", m, tb.scanned, removed+1)
		}
	}
}
