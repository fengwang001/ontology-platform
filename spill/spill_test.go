package spill

import (
	"fmt"
	"testing"
)

// TestResidentLimit 表驱动：Evict 始终搬走 LRU 最久未访问组，
// 搬后常驻数减一、该 Key 只在溢写区；多档规模循环生成。
func TestResidentLimit(t *testing.T) {
	cases := []struct {
		name   string
		puts   []string // 顺序插入；重复 Key 表示访问刷新
		evicts []string // 依次期望被溢写的 Key
	}{
		{"four-insert", []string{"a", "b", "c", "d"}, []string{"a"}},
		{"touch-b-refresh", []string{"a", "b", "c", "b", "d"}, []string{"a"}},
		{"two-evictions", []string{"a", "b", "c", "d", "e"}, []string{"a", "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New()
			seen := map[string]int64{"a": 5, "b": 3, "c": 7, "d": 2, "e": 9}
			for _, k := range tc.puts {
				if st, v := s.Lookup(k); st == Resident {
					s.PutResident(k, v+1)
				} else {
					s.PutResident(k, seen[k])
				}
			}
			before := s.ResidentLen()
			for _, want := range tc.evicts {
				got, ok := s.Evict()
				if !ok || got != want {
					t.Fatalf("Evict = %q,%v want %q", got, ok, want)
				}
				before--
				if s.ResidentLen() != before {
					t.Fatalf("ResidentLen = %d want %d", s.ResidentLen(), before)
				}
				r, sp := s.Snapshot()
				if _, inR := r[got]; inR {
					t.Fatalf("%s still resident after eviction", got)
				}
				if _, inSp := sp[got]; !inSp {
					t.Fatalf("%s missing from spilled after eviction", got)
				}
			}
		})
	}
	// 多档规模：m 个全新 Key 插入后逐个 Evict，必须严格按插入序。
	for _, m := range []int{100, 1000, 5000} {
		s := New()
		for i := 0; i < m; i++ {
			s.PutResident(fmt.Sprintf("k%05d", i), int64(i))
		}
		for i := 0; i < m; i++ {
			got, _ := s.Evict()
			if want := fmt.Sprintf("k%05d", i); got != want {
				t.Fatalf("m=%d evict#%d = %q want %q", m, i, got, want)
			}
		}
	}
}

// TestReloadMerge 表驱动：回载值 = 溢写部分和 + 新值，溢写条目删除、Key 只在常驻区。
func TestReloadMerge(t *testing.T) {
	cases := []struct {
		partial, delta, want int64
	}{
		{5, 1, 6},
		{0, 7, 7},
		{100, -30, 70},
	}
	for _, tc := range cases {
		s := New()
		s.PutResident("x", tc.partial)
		if _, ok := s.Evict(); !ok {
			t.Fatal("evict failed")
		}
		if got := s.LoadIn("x", tc.delta); got != tc.want {
			t.Fatalf("LoadIn = %d want %d", got, tc.want)
		}
		if st, v := s.Lookup("x"); st != Resident || v != tc.want {
			t.Fatalf("after load x = (%v,%d) want (Resident,%d)", st, v, tc.want)
		}
		r, sp := s.Snapshot()
		if _, inSp := sp["x"]; inSp {
			t.Fatal("x still in spilled after load")
		}
		if _, inR := r["x"]; !inR {
			t.Fatal("x missing from resident after load")
		}
	}
}

// TestProbeConstant 复杂度钉死：常驻 m 组后再喂 m+1 个全新 Key，
// 每次溢写检查个数恒为 1（直接读非导出字段，不经任何导出方法），不随 m 线性增长。
func TestProbeConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s := New()
		for i := 0; i < m; i++ {
			s.PutResident(fmt.Sprintf("r%d", i), int64(i))
		}
		for i := 0; i < m+1; i++ {
			s.PutResident(fmt.Sprintf("n%d", i), int64(i))
			if s.ResidentLen() > m {
				s.Evict()
			}
			if s.lastProbe != 1 {
				t.Fatalf("m=%d step=%d probe = %d, want 1", m, i, s.lastProbe)
			}
		}
	}
}
