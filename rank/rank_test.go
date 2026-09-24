package rank

import (
	"fmt"
	"math/bits"
	"testing"
)

func TestOrdering(t *testing.T) {
	cases := []struct {
		name string
		rows []Row
		want []Row
	}{
		{"score desc", []Row{{"a", 1}, {"b", 3}, {"c", 2}},
			[]Row{{"b", 3}, {"c", 2}, {"a", 1}}},
		{"tie key asc", []Row{{"c", 5}, {"a", 5}, {"b", 5}},
			[]Row{{"a", 5}, {"b", 5}, {"c", 5}}},
		{"mixed", []Row{{"e", 50}, {"b", 70}, {"d", 60}, {"c", 50}, {"a", 50}},
			[]Row{{"b", 70}, {"d", 60}, {"a", 50}, {"c", 50}, {"e", 50}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := New()
			for _, r := range tc.rows {
				o.Insert(r)
			}
			got := o.Snapshot()
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("pos %d = %v, want %v", i, got[i], tc.want[i])
				}
			}
			for i := 1; i < len(got); i++ {
				if !Less(got[i-1], got[i]) {
					t.Fatalf("not strictly ordered at %d: %v %v", i, got[i-1], got[i])
				}
			}
		})
	}
}

func TestInsertRemoveLookup(t *testing.T) {
	o := New()
	o.Insert(Row{"a", 1})
	o.Insert(Row{"b", 2})
	if r, ok := o.Lookup("a"); !ok || r.Score != 1 {
		t.Fatalf("lookup a = %v,%v", r, ok)
	}
	r, ok := o.Remove("a")
	if !ok || r.Key != "a" || o.Has("a") || o.Len() != 1 {
		t.Fatalf("remove failed: %v %v len=%d", r, ok, o.Len())
	}
	if _, ok := o.Remove("ghost"); ok {
		t.Fatal("remove ghost should fail")
	}
	if got := o.Snapshot(); len(got) != 1 || got[0].Key != "b" {
		t.Fatalf("snapshot after remove = %v", got)
	}
}

// TestComplexityBound 钉住第四节：单次进榜新增 / 榜内撤回的比较个数
// 不得超过 4·⌈log₂(m+1)⌉+4，即不随 m 线性增长。
func TestComplexityBound(t *testing.T) {
	for _, m := range []int{100, 500, 2000, 10000} {
		o := New()
		for i := 0; i < m; i++ { // Key 互异，部分分数并列
			o.Insert(Row{Key: fmt.Sprintf("k%05d", i), Score: int64(i % 50)})
		}
		lim := 4*bits.Len(uint(m)) + 4
		o.Insert(Row{Key: "~top", Score: 1 << 40}) // 必然进榜
		if o.cmpCount > lim {
			t.Fatalf("m=%d insert cmps=%d > bound=%d", m, o.cmpCount, lim)
		}
		top := o.At(0)
		o.Remove(top.Key) // 撤回榜内行
		if o.cmpCount > lim {
			t.Fatalf("m=%d remove cmps=%d > bound=%d", m, o.cmpCount, lim)
		}
	}
}
